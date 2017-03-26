package bitcoin

import (
	"encoding/json"
)

func UnmarshalMsg(buffer []byte) *Message {
	var msg Message
	err := json.Unmarshal(buffer, &msg)
	if err != nil {
		return nil
	}
	return &msg
}

func MarshalMsg(msg *Message) []byte {
	buffer, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	return buffer
}
