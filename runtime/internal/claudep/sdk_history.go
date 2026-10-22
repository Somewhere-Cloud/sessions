package claudep

import (
	"encoding/json"
	"time"
)

func stampSDKOperation(raw json.RawMessage, operation string) json.RawMessage {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	value["sessions_operation_id"] = operation
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return encoded
}

func parseSDKEvent(raw json.RawMessage, session string) (Event, error) {
	event, err := parseStreamJSONLine(raw, time.Now(), session)
	if err != nil {
		return event, err
	}
	var value map[string]any
	if err := json.Unmarshal(event.Raw, &value); err != nil {
		return Event{}, err
	}
	value["transport"] = "agent-sdk"
	event.Raw, err = json.Marshal(value)
	return event, err
}

// SDKObservation retains background evidence without promoting an unbound
// result to the foreground's completion. Existing history source stays wire
// compatible; transport identifies the actual producer.
func SDKObservation(raw json.RawMessage, session string) json.RawMessage {
	var frame sdkFrame
	if json.Unmarshal(raw, &frame) != nil {
		return nil
	}
	if frame.Type == "event" && !sdkIsResult(frame.Event) {
		event, err := parseSDKEvent(frame.Event, session)
		if err == nil {
			return event.Raw
		}
		return nil
	}
	if frame.Type != "state" && frame.Type != "transport_error" && frame.Type != "command_error" && !sdkIsResult(frame.Event) {
		return nil
	}
	value, _ := json.Marshal(map[string]any{
		"type": "system", "subtype": "sdk_observation", "source": "sessions",
		"transport": "agent-sdk", "session_id": session,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "observation": json.RawMessage(raw),
	})
	return value
}
