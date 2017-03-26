package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/cmu440/bitcoin"
	"github.com/cmu440/lsp"
)

const (
	rangePerMiner = 50000
)

type server struct {
	master    *applicationMaster
	lspServer lsp.Server
	exitChan  chan struct{}
}

type applicationMaster struct {
	curJobID      int           // # of jobs so far
	freeMinerList map[int]bool  // free miner to be assigned, key = miner connID
	busyMinerList map[int]*task // runnig task, key = miner connID
	jobList       map[int]*job  // all jobs from clients, key = jobID
	askList       map[int]*job  // jobs with requests for miners, key = jobID
}

type hashResult struct {
	hash  uint64
	nonce uint64
}

// a job is a request from client, and will be divided into tasks
type job struct {
	jobID           int
	clientID        int // connID of client
	str             string
	lower           uint64
	upper           uint64
	isValid         bool
	result          *hashResult
	unlaunchedTasks map[int]*task
	runningTasks    map[int]*task
	finishedTasks   map[int]*task
}

// task for miners
type task struct {
	taskID int
	jobID  int
	lower  uint64
	upper  uint64
	result *hashResult
}

func startServer(port int) (*server, error) {
	s, err := lsp.NewServer(port, lsp.NewParams())
	if err != nil {
		return nil, err
	} else {
		master := &applicationMaster{
			curJobID:      0,
			freeMinerList: make(map[int]bool),
			busyMinerList: make(map[int]*task),
			jobList:       make(map[int]*job),
			askList:       make(map[int]*job),
		}
		return &server{
			lspServer: s, exitChan: make(chan struct{}), master: master}, nil

	}

}

var LOGF *log.Logger

func main() {
	// You may need a logger for debug purpose
	const (
		name = "log.txt"
		flag = os.O_RDWR | os.O_CREATE
		perm = os.FileMode(0666)
	)

	file, err := os.OpenFile(name, flag, perm)
	if err != nil {
		return
	}
	defer file.Close()

	LOGF = log.New(file, "", log.Lshortfile|log.Lmicroseconds)
	// Usage: LOGF.Println() or LOGF.Printf()

	const numArgs = 2
	if len(os.Args) != numArgs {
		fmt.Printf("Usage: ./%s <port>", os.Args[0])
		return
	}

	port, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Println("Port must be a number:", err)
		return
	}

	srv, err := startServer(port)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Println("Server listening on port", port)

	defer srv.lspServer.Close()
	defer close(srv.exitChan)

	srv.RecvMsg()

}

func (s *server) SendMsg(connID int, msg *bitcoin.Message) error {
	payload := bitcoin.MarshalMsg(msg)
	err := s.lspServer.Write(connID, payload)
	return err
}

func (s *server) RecvMsg() {
	for {
		select {
		case <-s.exitChan:
			return
		default:
			connID, payload, err := s.lspServer.Read()
			if err != nil { // client/miner lost error
				s.handleRecvErr(connID)
			} else {
				msg := bitcoin.UnmarshalMsg(payload)
				if msg == nil {
					LOGF.Fatalln("Unmarshal Err: message structure mismatch")
					continue
				}
				switch msg.Type {
				case bitcoin.Join:
					s.handleJoinMsg(connID)
				case bitcoin.Request:
					s.handleRequestMsg(connID, msg)
				case bitcoin.Result:
					s.handleResultMsg(connID, msg)
				default:
					// ignore unknown type
				}
			}
			s.allocate()

		}

	}
}

func (s *server) handleRecvErr(connID int) {
	if s.master.freeMinerList[connID] {
		delete(s.master.freeMinerList, connID)

	} else if t, ok := s.master.busyMinerList[connID]; ok {
		// reassign the job to other miner
		j := s.master.jobList[t.jobID]
		delete(j.runningTasks, t.taskID)
		delete(s.master.busyMinerList, connID)
		j.unlaunchedTasks[t.taskID] = t

	} else {
		// if not miner lost, then client lost
		// cease proceeding with the request and ignore any result from miner
		for _, j := range s.master.jobList {
			if j.clientID == connID {
				j.isValid = false
				delete(s.master.askList, j.jobID)
			}
		}

	}
	s.lspServer.CloseConn(connID)

}

func (s *server) handleJoinMsg(connID int) {
	s.master.freeMinerList[connID] = true
}

func (s *server) handleRequestMsg(connID int, msg *bitcoin.Message) {

	str := msg.Data
	lower := msg.Lower
	upper := msg.Upper

	//create new job
	j := s.newJob(connID, str, lower, upper)
	s.master.jobList[j.jobID] = j
	s.master.askList[j.jobID] = j

}

func (s *server) handleResultMsg(connID int, msg *bitcoin.Message) {
	hash := msg.Hash
	nonce := msg.Nonce

	// combine the result and return to client
	t := s.master.busyMinerList[connID]
	t.result = &hashResult{hash, nonce}

	j := s.master.jobList[t.jobID]
	if j.isValid {
		j.finishedTasks[t.taskID] = t
		delete(j.runningTasks, t.taskID)

		// if no more task in the job, return result to client
		if len(j.unlaunchedTasks) == 0 && len(j.runningTasks) == 0 {
			for _, t := range j.finishedTasks {
				if j.result == nil || t.result.hash < j.result.hash {
					j.result = t.result
				}
			}
			msg := bitcoin.NewResult(j.result.hash, j.result.nonce)
			err := s.SendMsg(j.clientID, msg)
			// if err in SendMsg, meaning client lost
			if err != nil {
				j.isValid = false
				s.lspServer.CloseConn(j.clientID)
			}
			// job should be removed in askList
			delete(s.master.askList, j.jobID)

		}
	}

	// release miner resource
	s.master.freeMinerList[connID] = true
	delete(s.master.busyMinerList, connID)
}

func (s *server) newJob(clientID int, str string,
	lower uint64, upper uint64) *job {
	s.master.curJobID++
	j := &job{
		jobID:           s.master.curJobID,
		clientID:        clientID,
		str:             str,
		lower:           lower,
		upper:           upper,
		isValid:         true,
		result:          nil,
		unlaunchedTasks: make(map[int]*task),
		runningTasks:    make(map[int]*task),
		finishedTasks:   make(map[int]*task),
	}
	curTaskID := 0
	var i uint64
	for i = 0; i <= upper; i = i + rangePerMiner {
		t := &task{taskID: curTaskID, jobID: j.jobID, lower: i, result: nil}
		if i+rangePerMiner-1 <= upper {
			t.upper = i + rangePerMiner - 1
		} else {
			t.upper = upper
		}
		j.unlaunchedTasks[t.taskID] = t
		curTaskID++
	}
	return j
}

// Fair Scheduler:
// 1. Calculate averagely how many miners a job can have.
// 2. Looping through the askList of jobs, if currently running tasks < avg,
// and unlaunched tasks > 0, then assign a free miner to this job.
// If no free miner, then exit allocation
func (s *server) allocate() {
	minerCnt := len(s.master.freeMinerList) + len(s.master.busyMinerList)
	askCnt := len(s.master.askList)
	if askCnt == 0 {
		return
	}
	avg := minerCnt / askCnt
	if avg*askCnt < minerCnt {
		avg++
	}

	for _, j := range s.master.askList {
		for len(j.runningTasks) < avg && len(j.unlaunchedTasks) > 0 {
			t := j.getUnlaunchedTask()
			connID := s.master.getFreeMiner()
			if connID == -1 {
				return
			}
			msg := bitcoin.NewRequest(j.str, t.lower, t.upper)
			err := s.SendMsg(connID, msg)
			if err == nil {
				// if miner lost, this error can be also captured in recv
				// so ignore it
				j.runningTasks[t.taskID] = t
				delete(j.unlaunchedTasks, t.taskID)
				s.master.busyMinerList[connID] = t
				delete(s.master.freeMinerList, connID)
			} else {
				delete(s.master.freeMinerList, connID)
				s.lspServer.CloseConn(connID)
			}

		}

	}
}

func (a *applicationMaster) getFreeMiner() int {
	for k, _ := range a.freeMinerList {
		return k
	}
	return -1 // if len(a.freeMinerList) == 0
}

func (j *job) getUnlaunchedTask() *task {
	for _, v := range j.unlaunchedTasks {
		return v
	}
	return nil // 	if len(j.unlaunchedTasks) == 0
}
