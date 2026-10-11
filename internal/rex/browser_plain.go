package rex

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// plainBrowser is a browser the user drives, with a DevTools pipe Rex uses
// only when the user hands the browser back.
type plainBrowser struct {
	command *exec.Cmd
	send    *os.File
	exited  chan struct{}
	mu      sync.Mutex
	nextID  int
	waiting map[int]chan json.RawMessage
}

func startPlainBrowser(executable string, args []string) (*plainBrowser, error) {
	// Chrome reads commands from fd 3 and writes replies to fd 4.
	commandsRead, commandsWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	repliesRead, repliesWrite, err := os.Pipe()
	if err != nil {
		commandsRead.Close()
		commandsWrite.Close()
		return nil, err
	}
	command := exec.Command(executable, args...)
	command.ExtraFiles = []*os.File{commandsRead, repliesWrite}
	err = command.Start()
	commandsRead.Close()
	repliesWrite.Close()
	if err != nil {
		commandsWrite.Close()
		repliesRead.Close()
		return nil, err
	}
	browser := &plainBrowser{command: command, send: commandsWrite, exited: make(chan struct{}), waiting: map[int]chan json.RawMessage{}}
	go browser.readReplies(bufio.NewReader(repliesRead))
	go func() {
		_ = command.Wait()
		commandsWrite.Close()
		repliesRead.Close()
		close(browser.exited)
	}()
	return browser, nil
}

// readReplies hands each reply to the call waiting for it. Messages are
// JSON terminated by a NUL byte; events are ignored.
func (p *plainBrowser) readReplies(replies *bufio.Reader) {
	for {
		raw, err := replies.ReadBytes(0)
		if err != nil {
			return
		}
		var head struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(raw[:len(raw)-1], &head) != nil || head.ID == 0 {
			continue
		}
		p.mu.Lock()
		waiter := p.waiting[head.ID]
		delete(p.waiting, head.ID)
		p.mu.Unlock()
		if waiter != nil {
			waiter <- raw[:len(raw)-1]
		}
	}
}

// call sends one DevTools command and waits for its reply.
func (p *plainBrowser) call(method string, params any, deadline time.Time) (json.RawMessage, error) {
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	reply := make(chan json.RawMessage, 1)
	p.waiting[id] = reply
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.waiting, id)
		p.mu.Unlock()
	}()
	message, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	_ = p.send.SetWriteDeadline(deadline)
	if _, err := p.send.Write(append(message, 0)); err != nil {
		return nil, err
	}
	select {
	case raw := <-reply:
		var result struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		if result.Error != nil {
			return nil, errors.New(result.Error.Message)
		}
		return result.Result, nil
	case <-time.After(time.Until(deadline)):
		return nil, errors.New("the browser did not answer")
	case <-p.exited:
		return nil, errors.New("the browser has quit")
	}
}

// currentURL is the web page in the user's window, if any.
func (p *plainBrowser) currentURL(deadline time.Time) string {
	raw, err := p.call("Target.getTargets", map[string]any{}, deadline)
	if err != nil {
		return ""
	}
	var result struct {
		TargetInfos []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"targetInfos"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	// Targets are listed most recently used first.
	for _, target := range result.TargetInfos {
		if target.Type == "page" && (strings.HasPrefix(target.URL, "http://") || strings.HasPrefix(target.URL, "https://")) {
			return target.URL
		}
	}
	return ""
}

// close reads the open page, then quits the browser the way the user would,
// and kills it if it does not quit in time.
func (p *plainBrowser) close(timeout time.Duration) string {
	select {
	case <-p.exited:
		return ""
	default:
	}
	deadline := time.Now().Add(timeout)
	url := p.currentURL(time.Now().Add(3 * time.Second))
	_, _ = p.call("Browser.close", map[string]any{}, deadline)
	if !p.waitExit(time.Until(deadline)) {
		_ = p.command.Process.Kill()
		p.waitExit(3 * time.Second)
	}
	return url
}

func (p *plainBrowser) waitExit(timeout time.Duration) bool {
	select {
	case <-p.exited:
		return true
	default:
	}
	select {
	case <-p.exited:
		return true
	case <-time.After(timeout):
		return false
	}
}
