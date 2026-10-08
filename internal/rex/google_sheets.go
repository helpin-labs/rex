package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Google Sheets' tools. Finding and reading run on their own; writing
// follows the bot's approvals. Rex asks for spreadsheets (read and write
// the user's spreadsheets) and drive.metadata.readonly, which lets it find
// a spreadsheet by name without reading any other file's contents.

const (
	sheetsScope        = "https://www.googleapis.com/auth/spreadsheets"
	driveMetadataScope = "https://www.googleapis.com/auth/drive.metadata.readonly"
	// A read returns at most this many cells; the model asks for a
	// narrower range for more.
	sheetsCellLimit = 5000
)

var sheetsService = &googleService{
	name:        "Google Sheets",
	permissions: []string{sheetsScope, driveMetadataScope},
	tools: []googleTool{
		{
			name:        "find_spreadsheets",
			description: "Find the user's spreadsheets by name, most recently changed first. Empty query lists the latest ones.",
			access:      "read",
			schema: objectSchema(nil, map[string]any{
				"query":       map[string]any{"type": "string", "description": "Words in the spreadsheet's name."},
				"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "20 by default."},
			}),
			call: sheetsFind,
		},
		{
			name:        "get_spreadsheet",
			description: "A spreadsheet's title and its sheets (tabs), with each sheet's size. Use it before reading to learn the sheet names.",
			access:      "read",
			schema: objectSchema([]string{"spreadsheet"}, map[string]any{
				"spreadsheet": spreadsheetArgument,
			}),
			call: sheetsGet,
		},
		{
			name:        "read_range",
			description: "Read cell values as shown in the sheet, row by row. Ranges use A1 notation: \"Sheet1\", \"Sheet1!A1:D50\", \"'Q3 plan'!B:B\".",
			access:      "read",
			schema: objectSchema([]string{"spreadsheet", "range"}, map[string]any{
				"spreadsheet": spreadsheetArgument,
				"range":       map[string]any{"type": "string"},
			}),
			call: sheetsRead,
		},
		{
			name:        "write_range",
			description: "Write rows of values into a range, replacing what is there. Values are entered as if typed: numbers, dates and formulas like =SUM(B2:B9) work.",
			access:      "write",
			schema: objectSchema([]string{"spreadsheet", "range", "values"}, map[string]any{
				"spreadsheet": spreadsheetArgument,
				"range":       map[string]any{"type": "string", "description": "Where the first row starts, e.g. \"Sheet1!A2\", or the whole range."},
				"values":      rowsArgument,
			}),
			call: func(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
				return sheetsWrite(ctx, api, input, false)
			},
		},
		{
			name:        "append_rows",
			description: "Add rows after the last row of a table. Values are entered as if typed.",
			access:      "write",
			schema: objectSchema([]string{"spreadsheet", "range", "values"}, map[string]any{
				"spreadsheet": spreadsheetArgument,
				"range":       map[string]any{"type": "string", "description": "The table, e.g. \"Sheet1\" or \"Sheet1!A:E\"."},
				"values":      rowsArgument,
			}),
			call: func(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
				return sheetsWrite(ctx, api, input, true)
			},
		},
		{
			name:        "create_spreadsheet",
			description: "Create a new spreadsheet in the user's Google Drive.",
			access:      "write",
			schema: objectSchema([]string{"title"}, map[string]any{
				"title":  map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
				"sheets": map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "string"}, "description": "Names for its sheets (tabs); one \"Sheet1\" by default."},
			}),
			call: sheetsCreate,
		},
	},
}

var (
	spreadsheetArgument = map[string]any{"type": "string", "description": "The spreadsheet's id or its docs.google.com link."}
	rowsArgument        = map[string]any{"type": "array", "minItems": 1, "maxItems": 2000, "description": "Rows, each a list of cell values.",
		"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}
)

var (
	spreadsheetLink = regexp.MustCompile(`/spreadsheets/d/([A-Za-z0-9_-]+)`)
	spreadsheetID   = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
)

// parseSpreadsheet accepts a spreadsheet's id or any link to it.
func parseSpreadsheet(value string) (string, error) {
	value = strings.TrimSpace(value)
	if match := spreadsheetLink.FindStringSubmatch(value); match != nil {
		return match[1], nil
	}
	if spreadsheetID.MatchString(value) {
		return value, nil
	}
	return "", fmt.Errorf("%q is not a spreadsheet id or link; use find_spreadsheets", value)
}

func spreadsheetURL(id string) string {
	return "https://docs.google.com/spreadsheets/d/" + id + "/edit"
}

// driveQueryValue quotes text for a Drive search.
func driveQueryValue(text string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(text) + "'"
}

func sheetsFind(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 20
	}
	search := "mimeType='application/vnd.google-apps.spreadsheet' and trashed=false"
	for _, word := range strings.Fields(args.Query) {
		search += " and name contains " + driveQueryValue(word)
	}
	query := url.Values{
		"q": {search}, "orderBy": {"modifiedTime desc"}, "pageSize": {fmt.Sprint(min(args.MaxResults, 50))},
		"fields":                    {"files(id,name,modifiedTime,webViewLink,owners(displayName))"},
		"supportsAllDrives":         {"true"},
		"includeItemsFromAllDrives": {"true"},
	}
	var list struct {
		Files []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			ModifiedTime string `json:"modifiedTime"`
			WebViewLink  string `json:"webViewLink"`
			Owners       []struct {
				DisplayName string `json:"displayName"`
			} `json:"owners"`
		} `json:"files"`
	}
	if err := api.do(ctx, http.MethodGet, driveAPI+"/files?"+query.Encode(), nil, &list); err != nil {
		return nil, err
	}
	spreadsheets := []map[string]any{}
	for _, file := range list.Files {
		item := map[string]any{"id": file.ID, "name": file.Name, "modified": file.ModifiedTime, "link": firstNonEmpty(file.WebViewLink, spreadsheetURL(file.ID))}
		if len(file.Owners) > 0 {
			item["owner"] = file.Owners[0].DisplayName
		}
		spreadsheets = append(spreadsheets, item)
	}
	return map[string]any{"spreadsheets": spreadsheets}, nil
}

func sheetsGet(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		Spreadsheet string `json:"spreadsheet"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	id, err := parseSpreadsheet(args.Spreadsheet)
	if err != nil {
		return nil, err
	}
	var spreadsheet struct {
		ID         string `json:"spreadsheetId"`
		URL        string `json:"spreadsheetUrl"`
		Properties struct {
			Title string `json:"title"`
		} `json:"properties"`
		Sheets []struct {
			Properties struct {
				Title string `json:"title"`
				Grid  struct {
					Rows    int `json:"rowCount"`
					Columns int `json:"columnCount"`
				} `json:"gridProperties"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	fields := "spreadsheetId,spreadsheetUrl,properties.title,sheets.properties(title,gridProperties(rowCount,columnCount))"
	if err := api.do(ctx, http.MethodGet, sheetsAPI+"/"+url.PathEscape(id)+"?fields="+url.QueryEscape(fields), nil, &spreadsheet); err != nil {
		return nil, err
	}
	sheets := []map[string]any{}
	for _, sheet := range spreadsheet.Sheets {
		sheets = append(sheets, map[string]any{"title": sheet.Properties.Title, "rows": sheet.Properties.Grid.Rows, "columns": sheet.Properties.Grid.Columns})
	}
	return map[string]any{"id": spreadsheet.ID, "title": spreadsheet.Properties.Title, "link": firstNonEmpty(spreadsheet.URL, spreadsheetURL(id)), "sheets": sheets}, nil
}

func sheetsRead(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		Spreadsheet string `json:"spreadsheet"`
		Range       string `json:"range"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	id, err := parseSpreadsheet(args.Spreadsheet)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Range) == "" {
		return nil, errors.New("range is required, e.g. \"Sheet1!A1:D50\"")
	}
	var values struct {
		Range  string  `json:"range"`
		Values [][]any `json:"values"`
	}
	endpoint := sheetsAPI + "/" + url.PathEscape(id) + "/values/" + url.PathEscape(args.Range) + "?valueRenderOption=FORMATTED_VALUE"
	if err := api.do(ctx, http.MethodGet, endpoint, nil, &values); err != nil {
		return nil, err
	}
	rows, truncated := limitCells(values.Values, sheetsCellLimit)
	result := map[string]any{"range": values.Range, "values": rows, "rows": len(values.Values)}
	if rows == nil {
		result["values"] = [][]any{}
	}
	if truncated {
		result["truncated"] = true
		result["note"] = fmt.Sprintf("Showing the first %d of %d rows; read a narrower range for the rest.", len(rows), len(values.Values))
	}
	return result, nil
}

// limitCells keeps whole rows up to about limit cells.
func limitCells(rows [][]any, limit int) ([][]any, bool) {
	cells := 0
	for index, row := range rows {
		cells += max(len(row), 1)
		if cells > limit {
			return rows[:index], true
		}
	}
	return rows, false
}

func sheetsWrite(ctx context.Context, api *googleAPI, input json.RawMessage, appending bool) (any, error) {
	var args struct {
		Spreadsheet string     `json:"spreadsheet"`
		Range       string     `json:"range"`
		Values      [][]string `json:"values"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	id, err := parseSpreadsheet(args.Spreadsheet)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Range) == "" || len(args.Values) == 0 {
		return nil, errors.New("range and values are required")
	}
	body := map[string]any{"range": args.Range, "majorDimension": "ROWS", "values": args.Values}
	base := sheetsAPI + "/" + url.PathEscape(id) + "/values/" + url.PathEscape(args.Range)
	if appending {
		var appended struct {
			Updates struct {
				Range string `json:"updatedRange"`
				Rows  int    `json:"updatedRows"`
			} `json:"updates"`
		}
		if err := api.do(ctx, http.MethodPost, base+":append?valueInputOption=USER_ENTERED&insertDataOption=INSERT_ROWS", body, &appended); err != nil {
			return nil, err
		}
		return map[string]any{"status": "appended", "range": appended.Updates.Range, "rows": appended.Updates.Rows, "link": spreadsheetURL(id)}, nil
	}
	var updated struct {
		Range string `json:"updatedRange"`
		Cells int    `json:"updatedCells"`
	}
	if err := api.do(ctx, http.MethodPut, base+"?valueInputOption=USER_ENTERED", body, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"status": "written", "range": updated.Range, "cells": updated.Cells, "link": spreadsheetURL(id)}, nil
}

func sheetsCreate(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		Title  string   `json:"title"`
		Sheets []string `json:"sheets"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Title) == "" {
		return nil, errors.New("title is required")
	}
	body := map[string]any{"properties": map[string]any{"title": strings.TrimSpace(args.Title)}}
	var sheets []map[string]any
	for _, name := range args.Sheets {
		if name = strings.TrimSpace(name); name != "" {
			sheets = append(sheets, map[string]any{"properties": map[string]any{"title": name}})
		}
	}
	if len(sheets) > 0 {
		body["sheets"] = sheets
	}
	var created struct {
		ID  string `json:"spreadsheetId"`
		URL string `json:"spreadsheetUrl"`
	}
	if err := api.do(ctx, http.MethodPost, sheetsAPI, body, &created); err != nil {
		return nil, err
	}
	return map[string]any{"status": "created", "id": created.ID, "link": firstNonEmpty(created.URL, spreadsheetURL(created.ID))}, nil
}
