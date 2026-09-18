package types

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalJSON accepts the boolean detail API and integer list API formats.
// Keep IsRecovered as an int so the MCP response remains consistently 0 or 1.
func (e *AlertHisEvent) UnmarshalJSON(data []byte) error {
	type eventAlias AlertHisEvent
	decoded := eventAlias(*e)
	wire := struct {
		*eventAlias
		Recovery json.RawMessage `json:"is_recovered"`
	}{eventAlias: &decoded}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.Recovery) != 0 {
		switch string(bytes.TrimSpace(wire.Recovery)) {
		case "true", "1":
			decoded.IsRecovered = 1
		case "false", "0":
			decoded.IsRecovered = 0
		default:
			return fmt.Errorf("is_recovered must be a boolean or integer 0/1")
		}
	}
	*e = AlertHisEvent(decoded)
	return nil
}
