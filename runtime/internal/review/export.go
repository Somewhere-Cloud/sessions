package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ObservationsContract is the lane observation projection this exporter reads.
const ObservationsContract = "sessions.lane-observations"

// ExportOptions carries what the caller explicitly supplies besides the
// observation record. Task and tail are included only when given.
type ExportOptions struct {
	Subject    string
	CapturedAt time.Time
	Producer   string
	Task       *Task
	Tail       *Tail
}

// ExportObservations turns one lane observation record (the `observations`
// object of `sessions --json status`, or a status/listing record containing
// it) into a sessions.review-snapshot/1 document. It maps the record field by
// field: each leaf becomes a fact with the source its block was observed
// from, every null becomes an explicit unknown, and any field outside the v1
// mapping is exported as unknown rather than given a guessed provenance.
func ExportObservations(record []byte, options ExportOptions) ([]byte, error) {
	observations, subject, err := locateObservations(record)
	if err != nil {
		return nil, err
	}
	if version, ok := observations["version"].(json.Number); !ok || version.String() != "1" {
		return nil, fmt.Errorf("observations version %v is not supported; this exporter reads %s version 1", observations["version"], ObservationsContract)
	}
	if options.Subject == "" {
		options.Subject = subject
	}
	if options.Subject == "" {
		return nil, fmt.Errorf("the record has no id; pass a subject")
	}
	capabilities := map[string]string{}
	if block, ok := observations["capabilities"].(map[string]any); ok {
		for key, value := range block {
			capabilities[key] = fmt.Sprint(value)
		}
	}
	facts := map[string]Fact{}
	for _, block := range sortedKeys(observations) {
		if block == "version" || block == "capabilities" {
			continue
		}
		exportBlock(facts, block, observations[block], capabilities, observations)
	}
	snapshot := Snapshot{Schema: SnapshotSchema, Subject: options.Subject, CapturedAt: options.CapturedAt.UTC().Format(time.RFC3339),
		Producer: firstNonEmpty(options.Producer, "sessions review snapshot"), SourceContract: &SourceContract{Name: ObservationsContract, Version: 1},
		Capabilities: capabilities, Facts: facts, Task: options.Task, Tail: options.Tail}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if _, err := DecodeSnapshot(encoded); err != nil {
		return nil, fmt.Errorf("exported snapshot failed validation: %w", err)
	}
	return encoded, nil
}

func locateObservations(record []byte) (map[string]any, string, error) {
	decoder := json.NewDecoder(bytes.NewReader(record))
	decoder.UseNumber()
	var top map[string]any
	if err := decoder.Decode(&top); err != nil {
		return nil, "", fmt.Errorf("observation record is not a JSON object: %w", err)
	}
	subject, _ := top["id"].(string)
	if nested, ok := top["observations"]; ok {
		observations, ok := nested.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("the record's observations are null; this lane has no observation projection to export")
		}
		return observations, subject, nil
	}
	if _, ok := top["version"]; ok {
		return top, subject, nil
	}
	return nil, "", fmt.Errorf("no observations object found; pass the output of `sessions --json status <session>`")
}

func exportBlock(facts map[string]Fact, block string, value any, capabilities map[string]string, all map[string]any) {
	source := blockSource(block, "", capabilities, all)
	if source == "" {
		facts[block] = Fact{Source: "derived", Unknown: true, Reason: "block is outside the v1 mapping; its provenance is unknown"}
		return
	}
	if value == nil {
		facts[block] = Fact{Source: source, Unknown: true, Reason: "null in observations (capability none, restored record, or not observed)"}
		return
	}
	if block == "observers" {
		exportObservers(facts, value)
		return
	}
	object, ok := value.(map[string]any)
	if !ok {
		facts[block] = Fact{Source: "derived", Unknown: true, Reason: "unexpected shape for a v1 block; not exported"}
		return
	}
	for _, field := range sortedKeys(object) {
		name := block + "." + field
		source := blockSource(block, field, capabilities, all)
		switch {
		case source == "":
			facts[name] = Fact{Source: "derived", Unknown: true, Reason: "field is outside the v1 mapping; its provenance is unknown"}
		case object[field] == nil:
			facts[name] = Fact{Source: source, Unknown: true, Reason: "null in observations"}
		default:
			encoded, _ := json.Marshal(object[field])
			facts[name] = Fact{Source: source, Value: encoded}
		}
	}
}

func exportObservers(facts map[string]Fact, value any) {
	list, _ := value.([]any)
	for index, item := range list {
		observer, _ := item.(map[string]any)
		name, _ := observer["name"].(string)
		if name == "" {
			name = fmt.Sprint(index)
		}
		for _, field := range sortedKeys(observer) {
			if field == "name" {
				continue
			}
			key := "observers." + name + "." + field
			if !v1Fields["observers"][field] {
				facts[key] = Fact{Source: "derived", Unknown: true, Reason: "field is outside the v1 mapping; its provenance is unknown"}
				continue
			}
			if observer[field] == nil {
				facts[key] = Fact{Source: "derived", Unknown: true, Reason: "null in observations"}
				continue
			}
			encoded, _ := json.Marshal(observer[field])
			facts[key] = Fact{Source: "derived", Value: encoded}
		}
	}
}

// v1Fields is the exact field list of each observation block in contract
// version 1. A field not listed here is exported as unknown: its provenance
// was never specified, so none is guessed.
var v1Fields = map[string]map[string]bool{
	"runner":  {"state": true, "since": true, "exit_code": true},
	"working": {"value": true, "source": true},
	"turn":    {"open": true, "started_at": true, "started_by": true, "provider_confirmed_at": true, "ended_at": true, "ended_by": true},
	"last": {"provider_record_at": true, "terminal_output_at": true, "transcript_record_at": true, "input_dispatched_at": true,
		"input_confirmed_by_provider_at": true},
	"tools":     {"open": true, "open_count": true, "last_started_at": true, "last_ended_at": true, "history_complete": true},
	"observers": {"name": true, "state": true, "detail": true, "since": true},
}

// blockSource is the v1 mapping from an observation field to the provenance
// vocabulary. "" means the field is not part of the mapping. field "" names
// the whole block.
func blockSource(block, field string, capabilities map[string]string, all map[string]any) string {
	if fields, ok := v1Fields[block]; !ok || (field != "" && !fields[field]) {
		return ""
	}
	observed := observedSource(capabilities["turn_boundaries"])
	switch block {
	case "runner":
		return "runner"
	case "turn":
		return observed
	case "tools":
		return observedSource(capabilities["tool_records"])
	case "working":
		if field == "source" {
			return "derived"
		}
		if field == "" || field == "value" {
			return workingSource(all)
		}
	case "last":
		return lastSource(field, observed)
	case "observers":
		return "derived"
	}
	return ""
}

func lastSource(field, observed string) string {
	switch field {
	case "", "provider_record_at", "transcript_record_at", "input_confirmed_by_provider_at":
		return observed
	case "terminal_output_at":
		return "terminal"
	case "input_dispatched_at":
		return "ledger"
	}
	return ""
}

func observedSource(capability string) string {
	switch capability {
	case "provider-stream", "transcript-file", "rollout-file":
		return capability
	}
	return "derived"
}

func workingSource(all map[string]any) string {
	working, _ := all["working"].(map[string]any)
	switch source, _ := working["source"].(string); source {
	case "provider-stream", "rollout-file":
		return source
	case "terminal-heuristic", "byte-activity":
		return "terminal"
	case "provider-retry":
		return "runner"
	}
	return "derived"
}

// BoundedTail keeps the last MaxTailBytes of text on a UTF-8 boundary and
// records how much was omitted, so a long excerpt is never silently cut.
func BoundedTail(text, provenance string) *Tail {
	omitted := 0
	if len(text) > MaxTailBytes {
		cut := len(text) - MaxTailBytes
		for cut < len(text) && (text[cut]&0xC0) == 0x80 {
			cut++
		}
		omitted, text = cut, text[cut:]
	}
	return &Tail{OptIn: true, Text: text, OmittedBytes: omitted, Provenance: provenance}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
