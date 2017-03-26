package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/cmu440/bitcoin"
	"github.com/cmu440/lsp"
)

func main() {
	const numArgs = 4
	if len(os.Args) != numArgs {
		fmt.Printf("Usage: ./%s <hostport> <message> <maxNonce>", os.Args[0])
		return
	}
	hostport := os.Args[1]
	message := os.Args[2]
	maxNonce, err := strconv.ParseUint(os.Args[3], 10, 64)
	if err != nil {
		fmt.Printf("%s is not a number.\n", os.Args[3])
		return
	}

	client, err := lsp.NewClient(hostport, lsp.NewParams())

	if err != nil {
		fmt.Println("Failed to connect to server:", err)
		return
	}

	defer client.Close()

	// send request to server
	req := bitcoin.NewRequest(message, 0, maxNonce)
	msg := bitcoin.MarshalMsg(req)
	if msg == nil {
		// unexpected coding error
		return
	}
	err = client.Write(msg)
	if err != nil {
		printDisconnected()
		return
	}

	// wait for result from server
	b, err := client.Read()
	if err != nil {
		fmt.Println(err)
		printDisconnected()
		return
	}
	res := bitcoin.UnmarshalMsg(b)
	if res == nil || res.Type != bitcoin.Result {
		// unexpected coding error
		return
	}
	printResult(res.Hash, res.Nonce)

}

// printResult prints the final result to stdout.
func printResult(hash, nonce uint64) {
	fmt.Println("Result", hash, nonce)
}

// printDisconnected prints a disconnected message to stdout.
func printDisconnected() {
	fmt.Println("Disconnected")
}
