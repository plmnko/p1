package main

import (
	"fmt"
	"math"
	"os"

	"github.com/cmu440/bitcoin"
	"github.com/cmu440/lsp"
)

// Attempt to connect miner as a client to the server.
func joinWithServer(hostport string) (lsp.Client, error) {
	c, err := lsp.NewClient(hostport, lsp.NewParams())
	return c, err
}

func main() {
	const numArgs = 2
	if len(os.Args) != numArgs {
		fmt.Printf("Usage: ./%s <hostport>", os.Args[0])
		return
	}

	hostport := os.Args[1]
	miner, err := joinWithServer(hostport)
	if err != nil {
		fmt.Println("Failed to join with server:", err)
		return
	}

	defer miner.Close()

	join := bitcoin.NewJoin()
	msg := bitcoin.MarshalMsg(join)
	if msg == nil {
		// unexpected coding error
		return
	}
	// send join request to server
	err = miner.Write(msg)
	if err != nil {
		// lost contact with server, just close itself
		return
	}

	for {
		// receive request from server
		b, err := miner.Read()
		if err != nil {
			// lost contact with server, just close itself
			return
		}
		req := bitcoin.UnmarshalMsg(b)
		if req == nil || req.Type != bitcoin.Request {
			// unexpected coding error
			return
		}

		str := req.Data
		lower := req.Lower
		upper := req.Upper

		// do mining
		var minhash uint64 = math.MaxUint64
		nonce := lower
		for i := lower; i <= upper; i++ {
			hash := bitcoin.Hash(str, i)
			if hash < minhash {
				minhash = hash
				nonce = i
			}
		}

		// send result to server
		res := bitcoin.NewResult(minhash, nonce)
		resMsg := bitcoin.MarshalMsg(res)
		if resMsg == nil {
			// unexpected coding error
			return
		}
		// send result to server
		err = miner.Write(resMsg)
		if err != nil {
			// lost contact with server, just close itself
			return
		}
	}
}
