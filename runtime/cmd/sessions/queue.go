package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const queueUsage = "usage: sessions queue <session-id> [list | edit <op> <text...> | cancel <op> | move <op> --to N | next <op>]"

type queueItem struct {
	OperationID string `json:"operation_id"`
	Text        string `json:"text"`
	State       string `json:"state"`
	Position    int    `json:"position,omitempty"`
	EnqueuedAt  string `json:"enqueued_at"`
	Edits       int    `json:"edits,omitempty"`
}

type queueResponse struct {
	SessionID    string          `json:"session_id"`
	Paused       bool            `json:"paused"`
	Items        []queueItem     `json:"items"`
	Code         string          `json:"code,omitempty"`
	Error        string          `json:"error,omitempty"`
	Receipt      json.RawMessage `json:"receipt,omitempty"`
	ReceiptError string          `json:"receipt_error,omitempty"`
}

type queueRequest struct {
	Action      string `json:"action"`
	OperationID string `json:"operation_id"`
	Text        string `json:"text,omitempty"`
	To          int    `json:"to,omitempty"`
}

func (a *app) cmdQueue(args []string) error {
	if len(args) == 0 || args[0] == "" {
		return fail(1, queueUsage)
	}
	id, err := a.resolveSessionID(args[0])
	if err != nil {
		return err
	}
	path := "/api/sessions/" + escapeID(id) + "/queue"
	listed, err := a.queueCall(http.MethodGet, path, nil)
	if err != nil || len(args) == 1 || args[1] == "list" {
		if err == nil && len(args) > 2 {
			return fail(1, queueUsage)
		}
		return a.queueResult(listed, err, "")
	}
	request, err := parseQueueRequest(args[1:], listed.Items)
	if err != nil {
		return err
	}
	changed, err := a.queueCall(http.MethodPost, path, request)
	return a.queueResult(changed, err, describeQueueChange(request))
}

func parseQueueRequest(args []string, items []queueItem) (queueRequest, error) {
	action := args[0]
	args = args[1:]
	to := 0
	if index := indexOf(args, "--to"); index >= 0 {
		if index+1 >= len(args) {
			return queueRequest{}, fail(1, "--to needs a position (1 is next)")
		}
		parsed, err := strconv.Atoi(args[index+1])
		if err != nil || parsed < 1 {
			return queueRequest{}, fail(1, "--to needs a position of 1 or more")
		}
		to = parsed
		args = append(append([]string(nil), args[:index]...), args[index+2:]...)
	}
	if len(args) == 0 || args[0] == "" {
		return queueRequest{}, fail(1, queueUsage)
	}
	request := queueRequest{Action: action, OperationID: resolveQueuedOperation(args[0], items)}
	switch {
	case action == "edit" && len(args) >= 2 && to == 0:
		request.Text = strings.Join(args[1:], " ")
	case action == "cancel" && len(args) == 1 && to == 0:
	case action == "move" && len(args) == 1 && to > 0:
		request.To = to
	case action == "next" && len(args) == 1 && to == 0:
		request.Action, request.To = "move", 1
	default:
		return queueRequest{}, fail(1, queueUsage)
	}
	return request, nil
}

func indexOf(args []string, value string) int {
	for index, arg := range args {
		if arg == value {
			return index
		}
	}
	return -1
}

// resolveQueuedOperation accepts a unique prefix of a listed operation id.
// Anything else is sent as given, so the runner answers for ids it no longer
// lists (dispatched or cancelled ones).
func resolveQueuedOperation(value string, items []queueItem) string {
	match := ""
	for _, item := range items {
		if item.OperationID == value {
			return value
		}
		if strings.HasPrefix(item.OperationID, value) {
			if match != "" {
				return value
			}
			match = item.OperationID
		}
	}
	if match == "" {
		return value
	}
	return match
}

func (a *app) queueCall(method, path string, body any) (queueResponse, error) {
	response, err := a.api.request(context.Background(), method, path, body, 0)
	if err != nil {
		return queueResponse{}, fail(2, "%s → %s", path, err)
	}
	var decoded queueResponse
	if json.Unmarshal(response.body, &decoded) != nil {
		return queueResponse{}, fail(2, "%s → %d %s", path, response.status, prefixBytes(response.body, 200))
	}
	if response.status >= 400 && decoded.Code == "" {
		// The daemon refused before the runner answered: unknown session,
		// a session without saved messages, or an old runner.
		if decoded.Error == "" {
			decoded.Error = fmt.Sprintf("%s → %d", path, response.status)
		}
		return queueResponse{}, fail(1, "%s", decoded.Error)
	}
	return decoded, nil
}

func (a *app) queueResult(result queueResponse, err error, done string) error {
	if err != nil {
		return err
	}
	if a.wantJSON {
		if writeErr := writeJSON(a.stdout, struct {
			OK bool `json:"ok"`
			queueResponse
		}{result.Error == "", result}, false); writeErr != nil {
			return writeErr
		}
		if result.Error != "" {
			return status(1)
		}
		return nil
	}
	if result.Error != "" {
		return fail(1, "%s", result.Error)
	}
	if done != "" {
		fmt.Fprintln(a.stdout, done)
		if result.ReceiptError != "" {
			fmt.Fprintln(a.stderr, "sessions: "+result.ReceiptError)
		}
	}
	return writeQueueList(a, result)
}

func describeQueueChange(request queueRequest) string {
	switch {
	case request.Action == "cancel":
		return "cancelled " + request.OperationID + "; it was never sent"
	case request.Action == "edit":
		return "edited " + request.OperationID
	case request.To == 1:
		return request.OperationID + " will be sent next"
	default:
		return fmt.Sprintf("moved %s to position %d", request.OperationID, request.To)
	}
}

func writeQueueList(a *app, result queueResponse) error {
	if len(result.Items) == 0 {
		_, err := fmt.Fprintln(a.stdout, "no saved messages")
		return err
	}
	if result.Paused {
		fmt.Fprintln(a.stdout, "saved messages are paused; `sessions retry` continues them")
	}
	for _, item := range result.Items {
		marker := strconv.Itoa(item.Position) + "."
		if item.State == "dispatched" {
			marker = "now"
		}
		edited := ""
		if item.Edits > 0 {
			edited = " (edited)"
		}
		line := strings.Join(strings.Fields(item.Text), " ")
		if len([]rune(line)) > 72 {
			line = string([]rune(line)[:71]) + "…"
		}
		fmt.Fprintf(a.stdout, "%-4s %s  %s%s\n", marker, item.OperationID, line, edited)
	}
	return nil
}
