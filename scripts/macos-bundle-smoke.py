#!/usr/bin/env python3
"""Start the actual app's bundled services with isolated test data and verify shutdown."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.request

contents = Path(sys.argv[1]).resolve() / 'Contents'
with tempfile.TemporaryDirectory(prefix='rex-macos-bundle-') as directory:
    root = Path(directory)
    data = root / 'data'
    binaries = contents / 'MacOS'
    env = dict(os.environ, REX_DATA_DIR=str(data), REX_WORKSPACE_ROOT=str(root / 'workspace'),
               REX_SECRETS='memory', REX_HOST_ADDR='127.0.0.1:0',
               AGENT_RUNTIME_BINARY=str(binaries / 'agent-runtime'),
               REX_EMBEDDER_BINARY=str(binaries / 'llama-server'),
               REX_EMBEDDING_MODEL=str(contents / 'Resources/resources/models/bekko-embedding-v1-a8m-Q8_0.gguf'))
    with (root / 'host.log').open('w+') as log:
        host = subprocess.Popen([binaries / 'rex-host'], env=env, stdout=log, stderr=log)
        children = []
        try:
            deadline = time.monotonic() + 180
            ready = False
            while time.monotonic() < deadline:
                if host.poll() is not None:
                    raise RuntimeError('Bundled host exited before readiness')
                connection = data / 'connection.json'
                if connection.exists():
                    try:
                        settings = json.loads(connection.read_text())
                        request = urllib.request.Request(settings['url'] + '/api/status', headers={'Authorization': 'Bearer ' + settings['token']})
                        with urllib.request.urlopen(request, timeout=3) as response:
                            status = json.load(response)
                        if status['runtime_ready'] and status['memory']['available'] and status['memory']['status'] == 'ready':
                            ready = True
                            break
                    except (OSError, ValueError):
                        pass
                time.sleep(0.5)
            assert ready, 'Bundled runtime and embedding model did not become ready'
            for name in ('runtime.pid', 'embedder.pid'):
                children.append(int((data / name).read_text()))
            print('Bundled macOS host, runtime and embedding model are ready.')
        except Exception:
            log.flush()
            log.seek(0)
            print(log.read()[-16000:], file=sys.stderr)
            raise
        finally:
            if host.poll() is None:
                host.terminate()
                try:
                    host.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    host.kill()
                    host.wait(timeout=5)
                    raise RuntimeError('Bundled host did not stop gracefully')
        for pid in children:
            command = subprocess.run(['ps', '-p', str(pid), '-o', 'command='], capture_output=True, text=True).stdout
            assert str(binaries) not in command, f'Owned sidecar survived host shutdown: {pid}'
        print('Bundled services stopped cleanly.')
