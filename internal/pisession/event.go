package pisession

import "encoding/json"

type Event struct {
	Type string
	Raw  json.RawMessage
}
