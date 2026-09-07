package state

import (
	"encoding/json"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/providers"
)

func encodeToolCalls(calls []providers.ToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	data, err := json.Marshal(calls)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeToolCalls(data string, calls *[]providers.ToolCall) error {
	if err := json.Unmarshal([]byte(data), calls); err != nil {
		return err
	}
	return nil
}
