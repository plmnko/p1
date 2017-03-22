package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
)

const (
	MAX_MSG_SIZE = 1000
)

var (
	msgTooLargeErr    = errors.New("Msg too large")
	undefMsgTypeErr   = errors.New("Undefined Msg Type")
	clientLostErr     = errors.New("Client connection lost")
	clientClosedErr   = errors.New("Client conection closed")
	clientNotExistErr = errors.New("Client not existed")
	serverCloseErr    = errors.New("Server already closed")
	connFailErr       = errors.New("Connection failed")
	logger            = log.New(os.Stderr, "", log.Lshortfile|log.Lmicroseconds)
)

type writeReq struct {
	connID  int
	payload []byte
}

type readRep struct {
	connID  int
	payload []byte
	err     error
}

func UnmarshalMsg(buffer []byte) *Message {
	var msg Message
	err := json.Unmarshal(buffer, &msg)
	if err != nil {
		fmt.Println(err)
		return nil
	}
	return &msg
}

func MarshalMsg(msg *Message) []byte {
	buffer, err := json.Marshal(msg)
	if err != nil {
		fmt.Println(err)
		return nil
	}
	return buffer
}
