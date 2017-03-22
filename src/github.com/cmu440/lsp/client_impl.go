// Contains the implementation of a LSP client.

package lsp

import (
	"github.com/cmu440/lspnet"
	"time"
)

type client struct {
	conn     *lspnet.UDPConn
	connFin  chan *struct{}
	closeSig bool
	connID   int

	// epoch
	epochLimit  int
	epochMillis int
	windowSize  int
	epochCnt    int
	lastEpoch   int
	epochChan   chan struct{}
	// msg from clients
	dataMsgChan chan *Message
	ackMsgChan  chan *Message
	// for Write()
	wReqChan chan writeReq
	wRepChan chan error
	// for Read()
	readBuffer []*readRep
	rReqFlag   bool
	rReqChan   chan struct{}
	rRepChan   chan *readRep
	//for Close()
	closeConnReqChan chan struct{}
	closeConnRepChan chan error
	exitChan         chan struct{}

	seqSend       int              // for seq of outgoing data msg
	seqRecv       int              // seq of data msg received
	seqAck        int              // msg acked so far
	writeBuffer   []*Message       // store msg not in window
	notAckMsgList map[int]*Message // store data sent but not acked
	recvDataList  map[int]*Message // store data msg received but not in order
}

// NewClient creates, initiates, and returns a new client. This function
// should return after a connection with the server has been established
// (i.e., the client has received an Ack message from the server in response
// to its connection request), and should return a non-nil error if a
// connection could not be made (i.e., if after K epochs, the client still
// hasn't received an Ack message from the server in response to its K
// connection requests).

// hostport is a colon-separated string identifying the server's host address
// and port number (i.e., "localhost:9999").
func NewClient(hostport string, params *Params) (Client, error) {
	//Connect udp
	addr, err := lspnet.ResolveUDPAddr("udp", hostport)
	if err != nil {
		return nil, err
	}
	conn, err := lspnet.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}

	c := &client{
		conn:             conn,
		connFin:          make(chan *struct{}),
		closeSig:         false,
		connID:           -1,
		epochCnt:         0,
		lastEpoch:        0,
		epochChan:        make(chan struct{}),
		dataMsgChan:      make(chan *Message),
		ackMsgChan:       make(chan *Message),
		wReqChan:         make(chan writeReq),
		wRepChan:         make(chan error),
		readBuffer:       make([]*readRep, 0),
		rReqFlag:         false,
		rReqChan:         make(chan struct{}),
		rRepChan:         make(chan *readRep),
		closeConnReqChan: make(chan struct{}),
		closeConnRepChan: make(chan error),
		exitChan:         make(chan struct{}),
		seqSend:          0,
		seqRecv:          0,
		seqAck:           0,
		writeBuffer:      make([]*Message, 0),
		notAckMsgList:    make(map[int]*Message),
		recvDataList:     make(map[int]*Message),
	}
	if params == nil {
		params = NewParams()
	}
	c.epochLimit = params.EpochLimit
	c.epochMillis = params.EpochMillis
	c.windowSize = params.WindowSize

	msg := NewConnect()
	sendMsgClient(msg, conn)
	c.notAckMsgList[0] = msg

	go c.mainRoutine()
	go c.receiveMsgRoutine()
	go c.epochRoutine()

	rep := <-c.connFin
	if rep == nil {
		conn.Close()
		return nil, connFailErr
	}
	return c, nil
}

func (c *client) handleAckMsg(msg *Message) {
	c.lastEpoch = c.epochCnt
	if _, ok := c.notAckMsgList[msg.SeqNum]; ok {
		delete(c.notAckMsgList, msg.SeqNum)

		if msg.SeqNum == 0 {
			c.connID = msg.ConnID
			c.connFin <- &struct{}{}
		} else if c.connID > 0 {
			// trigger sliding window shift
			c.seqAck++
			if len(c.writeBuffer) > 0 {
				m := c.writeBuffer[0]
				sendMsgClient(m, c.conn)
				// put this new sent msg into not ack list
				c.notAckMsgList[m.SeqNum] = m
				c.writeBuffer = c.writeBuffer[1:]

			}
		}
	}
	if c.closeSig &&
		len(c.notAckMsgList) == 0 && len(c.writeBuffer) == 0 {
		if c.rReqFlag {
			c.rRepChan <- &readRep{0, nil, clientClosedErr}
			c.rReqFlag = false
		}
		c.closeConnRepChan <- nil
	}
}

func (c *client) handleDataMsg(msg *Message) {
	c.lastEpoch = c.epochCnt
	if c.connID < 0 || c.closeSig {
		return
	}
	if len(msg.Payload) < msg.Size { // should be dropped as never received
		return
	} else if len(msg.Payload) > msg.Size {
		msg.Payload = msg.Payload[:msg.Size]
	}

	ackmsg := NewAck(c.connID, msg.SeqNum)
	sendMsgClient(ackmsg, c.conn)

	if msg.SeqNum == c.seqRecv+1 { // expected sequence
		c.seqRecv++
		c.readBuffer = append(c.readBuffer, &readRep{0, msg.Payload, nil})
		l := len(c.recvDataList)
		for i := 0; i < l; i++ {
			if m, ok := c.recvDataList[c.seqRecv+1]; ok {
				c.seqRecv++
				c.readBuffer = append(c.readBuffer, &readRep{0, m.Payload, nil})
				delete(c.recvDataList, c.seqRecv)
			} else {
				break
			}
		}
	} else if msg.SeqNum > c.seqRecv { // order mixed up
		c.recvDataList[msg.SeqNum] = msg
	} // else meaning already received

	if c.rReqFlag {
		c.handleReadReq()
	}
}

func (c *client) handleReadReq() {
	if c.closeSig {
		c.rReqFlag = false
		c.rRepChan <- &readRep{0, nil, clientClosedErr}
		return
	}
	if len(c.readBuffer) > 0 {
		c.rReqFlag = false
		c.rRepChan <- &readRep{0, c.readBuffer[0].payload, nil}

		// remove the msg
		c.readBuffer = c.readBuffer[1:]

	}
}

func (c *client) handleWriteReq(wq writeReq) {
	payload := wq.payload
	var err error = nil
	if c.closeSig {
		err = clientClosedErr
	}
	c.wRepChan <- err
	if err != nil {
		return
	}
	c.seqSend++
	msg := NewData(c.connID, c.seqSend, len(payload), payload)
	if msg.SeqNum <= c.seqAck+c.windowSize {
		c.notAckMsgList[msg.SeqNum] = msg
		sendMsgClient(msg, c.conn)
	} else {
		c.writeBuffer = append(c.writeBuffer, msg)
	}
}

func (c *client) mainRoutine() {
	for {
		select {
		case <-c.exitChan:
			return
		case msg := <-c.dataMsgChan: // chan closed at Close()
			c.handleDataMsg(msg)
		case msg := <-c.ackMsgChan:
			c.handleAckMsg(msg)
		case <-c.rReqChan:
			c.rReqFlag = true
			c.handleReadReq()
		case wq := <-c.wReqChan:
			c.handleWriteReq(wq)
		case <-c.epochChan:
			c.handleEpoch()
		case <-c.closeConnReqChan:
			c.closeSig = true
			if len(c.writeBuffer) == 0 && len(c.notAckMsgList) == 0 {
				if c.rReqFlag {
					c.rReqFlag = false
					c.rRepChan <- &readRep{0, nil, clientClosedErr}
				}
				c.closeConnRepChan <- nil
			}
		}
	}
}

// readFromUDP will block until some msg comes
// start a go routine to handle incoming msg
func (c *client) receiveMsgRoutine() {
	for {
		select {
		case <-c.exitChan:
			return
		default:
			msg := recvMsgClient(c.conn)
			if msg == nil { // ignore wrong msg
				continue
			}
			switch msg.Type {
			case MsgData:
				c.dataMsgChan <- msg
			case MsgAck:
				c.ackMsgChan <- msg
			default: //ignore undefined msg type
				logger.Println(undefMsgTypeErr)
			}
		}
	}
}

func (c *client) epochRoutine() {
	for {
		select {
		case <-c.exitChan:
			return
		case <-time.After(time.Duration(c.epochMillis) * time.Millisecond):
			c.epochChan <- struct{}{}
		}
	}
}

// handle epoch, invoked by mainRoutine:
// 1) If the client’s connection request has not yet been acknowledged by the server,
// resend the connection request.
// 2) If the connection request has been sent and acknowledged, but no data messages
// received, then send an acknowledgment with sequence number 0.
// 3) For each data message that has been sent but not yet acknowledged, resend
// the data message.
func (c *client) handleEpoch() {
	c.epochCnt++

	// if not receiving any msg more than epochLimit, then conn lost
	if c.epochCnt-c.lastEpoch > c.epochLimit {
		if c.rReqFlag {
			c.rReqFlag = false
			c.rRepChan <- &readRep{0, nil, clientLostErr}
		}
		if c.closeSig {
			c.closeConnRepChan <- clientLostErr
		}
		return
	}
	// 2) if no data msg have been received
	if c.connID > 0 && c.seqRecv == 0 {
		msg := NewAck(c.connID, 0)
		sendMsgClient(msg, c.conn)
	}
	// 1), 3) for every msg that sent but not acked, resend
	// connection msg is also included in the list
	for _, v := range c.notAckMsgList {
		sendMsgClient(v, c.conn)
	}

}

func (c *client) ConnID() int {
	return c.connID
}

// Read reads a data message from the server and returns its payload.
// This method should block until data has been received from the server and
// is ready to be returned. It should return a non-nil error if either
// (1) the connection has been explicitly closed, or
// (2) the connection has been lost due to an epoch timeout and no other
// messages are waiting to be returned.
func (c *client) Read() ([]byte, error) {
	c.rReqChan <- struct{}{}
	reply := <-c.rRepChan
	return reply.payload, reply.err
}

// Write sends a data message with the specified payload to the server.
// This method should NOT block, and should return a non-nil error
// if the connection with the server has been lost.
func (c *client) Write(payload []byte) error {
	if len(payload) > MAX_MSG_SIZE {
		return msgTooLargeErr
	}
	c.wReqChan <- writeReq{0, payload}
	err := <-c.wRepChan
	return err
}

// Close terminates the client's connection with the server. It should block
// until all pending messages to the server have been sent and acknowledged.
// Once it returns, all goroutines running in the background should exit.
//
// You may assume that Read, Write, and Close will not be called after
// Close has been called.
func (c *client) Close() error {
	c.closeConnReqChan <- struct{}{}
	err := <-c.closeConnRepChan
	close(c.exitChan)
	c.conn.Close()
	return err
}

func sendMsgClient(msg *Message, conn *lspnet.UDPConn) {
	b := MarshalMsg(msg)
	if b == nil {
		// suggest sth wrong with the implementation
		logger.Fatalln("Fatal error in Marshal at sendMsg")
	}
	// WriteToUDP is only good for server, on client should use Write
	// No error will be thrown and no blocking even if connection has lost
	conn.Write(b)
}

func recvMsgClient(conn *lspnet.UDPConn) *Message {
	buffer := make([]byte, 1024)
	size, err := conn.Read(buffer)
	if err != nil { // error in Read, might caused by connection closed
		return nil
	}
	msg := UnmarshalMsg(buffer[:size]) // if any error in unmarshal, msg = nil
	if msg == nil {
		logger.Println("Error in Unmarshal at recvMsg", size)
	}
	return msg
}
