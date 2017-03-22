// Contains the implementation of a LSP server.

package lsp

import (
	"github.com/cmu440/lspnet"
	"strconv"
	"time"
)

type server struct {
	conn          *lspnet.UDPConn
	curID         int // for connID
	clientCnt     int
	closeSig      bool
	clientAddrMap map[*lspnet.UDPAddr]bool // track addr and client
	clientIdMap   map[int]*clientInfo      // connID as key
	// epoch
	epochLimit  int
	epochMillis int
	windowSize  int
	epochCnt    int
	epochChan   chan struct{}
	// msg from clients
	connMsgChan chan *msgWrapper // connection msg has no content
	dataMsgChan chan *msgWrapper
	ackMsgChan  chan *msgWrapper
	// for Write()
	wReqChan chan writeReq
	wRepChan chan error
	// for Read()
	readBuffer []*readRep
	rReqFlag   bool
	rReqChan   chan struct{}
	rRepChan   chan *readRep
	//for closeConn()
	closeConnReqChan chan int
	closeConnRepChan chan error
	// for Close()
	shutdownAll    chan struct{}
	shutdownAllErr chan error
	shutdownAllFin chan struct{}
	exitChan       chan struct{}
}

type clientInfo struct {
	connID        int
	addr          *lspnet.UDPAddr
	closeSig      bool
	isLost        bool
	isClosed      bool
	seqSend       int              // number of data msg sent to client
	seqAck        int              // number of ack msg from client
	writeBuffer   []*Message       // store msg not in window
	notAckMsgList map[int]*Message // store data sent but not acked
	seqRecv       int              // number of data msg from client
	recvDataList  map[int]*Message // store data msg received but not in order
	lastEpoch     int              // compare with s.epochCnt to judge lost
}

// used to check clientList[msg.connID].addr == addr
// security issue: if an addr use another client's connID
type msgWrapper struct {
	msg  *Message
	addr *lspnet.UDPAddr
}

// NewServer creates, initiates, and returns a new server. This function should
// NOT block. Instead, it should spawn one or more goroutines (to handle things
// like accepting incoming client connections, triggering epoch events at
// fixed intervals, synchronizing events using a for-select loop like you saw in
// project 0, etc.) and immediately return. It should return a non-nil error if
// there was an error resolving or listening on the specified port number.
func NewServer(port int, params *Params) (Server, error) {
	// listen to incoming udp packets
	addr, err := lspnet.ResolveUDPAddr("udp", ":"+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	conn, err := lspnet.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	s := server{
		conn:             conn,
		curID:            0,
		clientCnt:        0,
		closeSig:         false,
		clientAddrMap:    make(map[*lspnet.UDPAddr]bool),
		clientIdMap:      make(map[int]*clientInfo),
		epochCnt:         0,
		epochChan:        make(chan struct{}),
		connMsgChan:      make(chan *msgWrapper),
		dataMsgChan:      make(chan *msgWrapper),
		ackMsgChan:       make(chan *msgWrapper),
		wReqChan:         make(chan writeReq),
		wRepChan:         make(chan error),
		readBuffer:       make([]*readRep, 0),
		rReqFlag:         false,
		rReqChan:         make(chan struct{}),
		rRepChan:         make(chan *readRep),
		closeConnReqChan: make(chan int),
		closeConnRepChan: make(chan error),
		shutdownAll:      make(chan struct{}),
		shutdownAllErr:   make(chan error),
		shutdownAllFin:   make(chan struct{}),
		exitChan:         make(chan struct{}),
	}

	if params == nil {
		params = NewParams()
	}
	s.epochLimit = params.EpochLimit
	s.epochMillis = params.EpochMillis
	s.windowSize = params.WindowSize

	// go routines
	go s.mainRoutine()
	go s.receiveMsgRoutine()
	go s.epochRoutine()
	return &s, nil
}

// invoked by mainRoutine, handle connection msg
func (s *server) handleConnMsg(wrapper *msgWrapper) {
	if s.closeSig {
		return
	}
	addr := wrapper.addr
	if s.clientAddrMap[addr] { // if already exist
		return
	}
	s.clientAddrMap[addr] = true
	s.curID++
	s.clientCnt++

	cli := &clientInfo{
		connID:        s.curID,
		addr:          addr,
		closeSig:      false,
		isLost:        false,
		isClosed:      false,
		seqSend:       0,
		seqAck:        0,
		seqRecv:       0,
		notAckMsgList: make(map[int]*Message),
		recvDataList:  make(map[int]*Message),
		writeBuffer:   make([]*Message, 0),
		lastEpoch:     s.epochCnt,
	}
	s.clientIdMap[s.curID] = cli
	msg := NewAck(cli.connID, 0)
	sendMsg(msg, addr, s.conn)
}

//invokde by mainRoutine, handle data msg
func (s *server) handleDataMsg(wrapper *msgWrapper) {
	addr := wrapper.addr
	msg := wrapper.msg
	cli := s.clientIdMap[msg.ConnID]
	cli.lastEpoch = s.epochCnt

	// check addr the same to avoid potential security issue
	if s.closeSig || cli.closeSig || cli.isLost || cli.isClosed ||
		cli.addr.String() != addr.String() {
		return
	}

	if len(msg.Payload) < msg.Size { // should be dropped as never received
		return
	} else if len(msg.Payload) > msg.Size {
		msg.Payload = msg.Payload[:msg.Size]
	}

	ackmsg := NewAck(msg.ConnID, msg.SeqNum)
	sendMsg(ackmsg, cli.addr, s.conn)

	if msg.SeqNum == cli.seqRecv+1 { // expected sequence
		cli.seqRecv++
		s.readBuffer = append(s.readBuffer,
			&readRep{msg.ConnID, msg.Payload, nil})

		l := len(cli.recvDataList)
		for i := 0; i < l; i++ {
			if m, ok := cli.recvDataList[cli.seqRecv+1]; ok {
				cli.seqRecv++
				s.readBuffer = append(s.readBuffer,
					&readRep{m.ConnID, m.Payload, nil})
				delete(cli.recvDataList, cli.seqRecv)
			} else {
				break
			}

		}
	} else if msg.SeqNum > cli.seqRecv { // order mixed up
		cli.recvDataList[msg.SeqNum] = msg
	} // else meaning already received

}

// invoked by mainRoutine, handle ack msg
func (s *server) handleAckMsg(wrapper *msgWrapper) {
	addr := wrapper.addr
	msg := wrapper.msg
	cli := s.clientIdMap[msg.ConnID]

	cli.lastEpoch = s.epochCnt
	if cli.isLost || cli.isClosed || cli.addr.String() != addr.String() {
		return
	}
	if _, ok := cli.notAckMsgList[msg.SeqNum]; ok {
		delete(cli.notAckMsgList, msg.SeqNum)
		// trigger sliding window shift
		cli.seqAck++
		if len(cli.writeBuffer) > 0 {
			m := cli.writeBuffer[0]
			sendMsg(m, cli.addr, s.conn)
			// put this new sent msg into not ack list
			cli.notAckMsgList[m.SeqNum] = m
			cli.writeBuffer = cli.writeBuffer[1:]

		}
	}
	if cli.closeSig &&
		len(cli.notAckMsgList) == 0 && len(cli.writeBuffer) == 0 {
		cli.isClosed = true
		s.clientCnt--
		s.readBuffer = append(s.readBuffer, &readRep{0, nil, clientClosedErr})
	}
}

// invoked by mainRoutine, handle Write() request
func (s *server) handleWriteReq(wq writeReq) {
	connID := wq.connID
	payload := wq.payload
	cli, err := s.getClientByConnID(connID)
	s.wRepChan <- err
	if err != nil {
		return
	}
	cli.seqSend++
	msg := NewData(connID, cli.seqSend, len(payload), payload)
	if msg.SeqNum <= cli.seqAck+s.windowSize {
		cli.notAckMsgList[msg.SeqNum] = msg
		sendMsg(msg, cli.addr, s.conn)
	} else {
		cli.writeBuffer = append(cli.writeBuffer, msg)
	}
}

// invoked by mainRoutine, handle Read() request
// either when request just received or when data received
func (s *server) handleReadReq() {
	if s.closeSig {
		s.rReqFlag = false
		s.rRepChan <- &readRep{0, nil, serverCloseErr}
		return
	}
	if len(s.readBuffer) > 0 {
		s.rReqFlag = false
		s.rRepChan <- s.readBuffer[0]
		// remove the msg
		s.readBuffer = s.readBuffer[1:]

	}
}

// handle epoch, invoked by mainRoutine:
// 1) If no data msg have been received from the client, then resend an ACK
// msg for the client’s connection request.
// 2) For each data msg that has been sent, but not yet ACK, resend the data msg.
func (s *server) handleEpoch() {
	s.epochCnt++
	for connID, cli := range s.clientIdMap {
		if !cli.isLost && !cli.isClosed {
			// if not receiving any msg more than epochLimit, then conn lost
			if s.epochCnt-cli.lastEpoch > s.epochLimit {
				if s.closeSig {
					s.shutdownAllErr <- clientLostErr
				}
				cli.isLost = true
				// only remove the addr so it can init a new connect
				delete(s.clientAddrMap, cli.addr)
				s.clientCnt--

				s.readBuffer = append(s.readBuffer,
					&readRep{0, nil, clientLostErr})
			}
			if !cli.isLost {
				// if no data msg have been received
				if cli.seqRecv == 0 {
					msg := NewAck(connID, 0)
					sendMsg(msg, cli.addr, s.conn)
				}
				// for every msg that sent but not acked, resend
				for _, v := range cli.notAckMsgList {
					sendMsg(v, cli.addr, s.conn)
				}
			}
		}
	}
}

func (s *server) handleConnClose(connID int) {
	cli, err := s.getClientByConnID(connID)
	s.closeConnRepChan <- err
	if err != nil {
		return
	}
	if len(cli.writeBuffer) == 0 && len(cli.notAckMsgList) == 0 {
		cli.isClosed = true
		s.clientCnt--
		s.readBuffer = append(s.readBuffer, &readRep{0, nil, clientLostErr})
	} else {
		cli.closeSig = true
	}
}

func (s *server) mainRoutine() {
	for {
		select {
		case <-s.exitChan:
			return
		case <-s.shutdownAll:
			s.closeSig = true
			for _, cli := range s.clientIdMap {
				if !cli.isClosed && !cli.isLost {
					cli.closeSig = true
				}
			}
		case addr := <-s.connMsgChan: // chan closed at Close()
			s.handleConnMsg(addr)
		case msg := <-s.dataMsgChan: // chan closed at Close()
			s.handleDataMsg(msg)
		case msg := <-s.ackMsgChan:
			s.handleAckMsg(msg)
		case <-s.rReqChan:
			s.rReqFlag = true
		case wq := <-s.wReqChan:
			s.handleWriteReq(wq)
		case <-s.epochChan:
			s.handleEpoch()
		case connID := <-s.closeConnReqChan:
			s.handleConnClose(connID)
		}
		if s.rReqFlag {
			s.handleReadReq()
		}
		if s.clientCnt == 0 && s.closeSig {
			s.shutdownAllFin <- struct{}{}
		}
	}
}

// readFromUDP will block until some msg comes
// start a go routine to handle incoming msg
func (s *server) receiveMsgRoutine() {
	for {
		select {
		case <-s.exitChan:
			return
		default:
			msg, addr := recvMsg(s.conn)
			if msg == nil { // ignore wrong msg
				continue
			}
			switch msg.Type {
			case MsgConnect:
				s.connMsgChan <- &msgWrapper{msg, addr}
			case MsgData:
				s.dataMsgChan <- &msgWrapper{msg, addr}
			case MsgAck:
				s.ackMsgChan <- &msgWrapper{msg, addr}
			default: //ignore undefined msg type
				logger.Println(undefMsgTypeErr)
			}
		}
	}
}

func (s *server) epochRoutine() {
	for {
		select {
		case <-s.exitChan:
			return
		case <-time.After(time.Duration(s.epochMillis) * time.Millisecond):
			s.epochChan <- struct{}{}
		}
	}
}

func (s *server) getClientByConnID(connID int) (*clientInfo, error) {
	if cli, ok := s.clientIdMap[connID]; ok {
		if cli.isLost {
			return nil, clientLostErr
		} else if cli.isClosed || cli.closeSig || s.closeSig {
			return nil, clientClosedErr
		} else {
			return cli, nil
		}
	} else {
		return nil, clientNotExistErr
	}
}

// Read reads a data message from a client and returns its payload,
// and the connection ID associated with the client that sent the message.
// This method should block until data has been received from some client.
// It should return a non-nil error if either (1) the connection to some
// client has been explicitly closed, (2) the connection to some client
// has been lost due to an epoch timeout and no other messages from that
// client are waiting to be returned, or (3) the server has been closed.
// In the first two cases, the client's connection ID and a non-nil
// error should be returned. In the third case, an ID with value 0 and
// a non-nil error should be returned.

// Close() desc said no Read after Close(), conflicting 3)
// Based on the test case in lsp3_test.go, all client lost need to be returned
// in Read, so if a client lost, then its lost err should be put into the read
// buffer for future Read() invoke.
func (s *server) Read() (int, []byte, error) {
	s.rReqChan <- struct{}{}
	reply := <-s.rRepChan
	return reply.connID, reply.payload, reply.err
}

// Write sends a data message to the client with the specified connection ID.
// This method should NOT block, and should return a non-nil error if the
// connection with the client has been lost.
func (s *server) Write(connID int, payload []byte) error {
	if len(payload) > MAX_MSG_SIZE {
		return msgTooLargeErr
	}
	s.wReqChan <- writeReq{connID, payload}
	err := <-s.wRepChan
	return err
}

// CloseConn terminates the client with the specified connection ID, returning
// a non-nil error if the specified connection ID does not exist. All pending
// messages to the client should be sent and acknowledged. However, unlike Close,
// this method should NOT block.
func (s *server) CloseConn(connID int) error {
	s.closeConnReqChan <- connID
	err := <-s.closeConnRepChan
	return err
}

// Close terminates all currently connected clients and shuts down the LSP server.
// This method should block until all pending messages for each client are sent
// and acknowledged. If one or more clients are lost during this time, a non-nil
// error should be returned. Once it returns, all goroutines running in the
// background should exit.
//
// You may assume that Read, Write, CloseConn, or Close will not be called after
// calling this method.
func (s *server) Close() error {
	// only receive ack msg from client, and send msg to client
	// not receiveing conn and data msg
	s.shutdownAll <- struct{}{}
	var err error = nil
	for {
		select {
		case err = <-s.shutdownAllErr:
		case <-s.shutdownAllFin:
			close(s.exitChan) // close several go routines
			s.conn.Close()
			return err
		}
	}

}

func sendMsg(msg *Message, addr *lspnet.UDPAddr, conn *lspnet.UDPConn) {
	b := MarshalMsg(msg)
	if b == nil {
		// suggest sth wrong with the implementation
		logger.Fatalln("Fatal error in Marshal at sendMsg")
	}
	// WriteToUDP is only good for server, on client should use Write
	// No error will be thrown and no blocking even if connection has lost
	conn.WriteToUDP(b, addr)
}

func recvMsg(conn *lspnet.UDPConn) (*Message, *lspnet.UDPAddr) {
	buffer := make([]byte, 1024)
	// ReadFromUDP blocks until something comes in
	size, addr, err := conn.ReadFromUDP(buffer)
	if err != nil { // error might caused by connection closed
		return nil, addr
	}
	msg := UnmarshalMsg(buffer[:size]) // if any error in unmarshal, msg = nil
	if msg == nil {
		logger.Println("Error in Unmarshal at recvMsg")
	}
	return msg, addr
}
