p1
==

This repository contains the starter code for project 1 (15-440, Fall 2016). It's a bit different
from the old version, eg. message has new field Size in the Fall2016 version. 

## Part A LSP Protocol

Have to use the given lspnet package instead of net package in order to trigger packet lost for 
testing. 

The `srunner` and `crunner` programs may be customized using command line flags. For more
information, specify the `-h` flag at the command line. For example,

```bash
$ go run srunner.go -h
Usage of bin/srunner:
  -elim=5: epoch limit
  -ems=2000: epoch duration (ms)
  -port=9999: port number
  -rdrop=0: network read drop percent
  -v=false: show srunner logs
  -wdrop=0: network write drop percent
  -wsize=1: window size
```

### Running the tests

To test your submission, we will execute the following command from inside the
`p1/src/github.com/cmu440/lsp` directory for each of the tests (where `TestName` is the
name of one of the 44 test cases, such as `TestBasic6` or `TestWindow1`):

```sh
go test -run=TestName
```

Note that we will execute each test _individually_ using the `-run` flag and by specify a regular expression
identifying the name of the test to run. To ensure that previous tests don’t affect the outcome of later tests,
we recommend executing the tests individually (or in small batches, such as `go test -run=TestBasic` which will
execute all tests beginning with `TestBasic`) as opposed to all together using `go test`.

On some tests, we will also check your code for race conditions using Go’s race detector:

```sh
go test -race -run=TestName
```

## Part B Bitcoin miner

### Importing the `bitcoin` package

In order to use the starter code we provide in the `hash.go` and `message.go` files, use the
following `import` statement:

```go
import "github.com/cmu440/bitcoin"
```

Once you do this, you should be able to make use of the `bitcoin` package as follows:

```go
hash := bitcoin.Hash("thom yorke", 19970521)

msg := bitcoin.NewRequest("jonny greenwood", 200, 71010)
```

### Compiling the `client`, `miner` & `server` programs

To compile the `client`, `miner`, and `server` programs, use the `go install` command
as follows (these instructions assume your
`GOPATH` is pointing to the project's root `p1/` directory):

```bash
# Compile the client, miner, and server programs. The resulting binaries
# will be located in the $GOPATH/bin directory.
go install github.com/cmu440/bitcoin/client
go install github.com/cmu440/bitcoin/miner
go install github.com/cmu440/bitcoin/server

# Start the server, specifying the port to listen on.
$GOPATH/bin/server 6060

# Start a miner, specifying the server's host:port.
$GOPATH/bin/miner localhost:6060

# Start the client, specifying the server's host:port, the message
# "bradfitz", and max nonce 9999.
$GOPATH/bin/client localhost:6060 bradfitz 9999
```

Note that you will need to use the `os.Args` variable in your code to access the user-specified command line arguments.

### Running the tests

Unlike in previous projects, the tests for part B will _not_ be open source. Instead, we have
provided two binaries&mdash;`ctest` and `mtest`&mdash;for you to use to test your code. On Autolab, we will be testing you using these two binaries along with an additional `stest` binary.

To execute the tests, make sure your `GOPATH` is properly set and then execute them as follows (note
that you for each binary, you can activate verbose-mode by specifying the `-v` flag). _Make sure you
compile your `client`, `miner`, and `server` programs using `go install` before running the tests!_

```bash
# Run ctest on a Linux machine in non-verbose mode.
$GOPATH/src/github.com/cmu440/bitcoin/tests/ctest
```

When you run the tests, one of the first things you'll probably notice is that none of the logs
you print in both the code you write for part A and part B will not appear. This is because
our test binaries must capture the output of your programs in order to test that your request clients
print the correct result message to standard output at the end of each test. An alternative to
logging messages to standard output is to use a `log.Logger` and direct them to a file instead, as
illustrated by the code below:

```go
const (
    name = "log.txt"
    flag = os.O_RDWR | os.O_CREATE
    perm = os.FileMode(0666)
)

file, err := os.OpenFile(name, flag, perm)
if err != nil {
    return
}

LOGF := log.New(file, "", log.Lshortfile|log.Lmicroseconds)
LOGF.Println("Bees?!", "Beads.", "Gob's not on board.")
```

Don't forget to call `file.Close()` when you are done using it!

## Miscellaneous

### Reading the API Documentation

Before you begin the project, you should read and understand all of the starter code we provide.
To make this experience a little less traumatic (we know, it's a lot :P),
fire up a web server and read the documentation in a browser by executing the following command:

```sh
godoc -http=:6060 &
```

Then, navigate to [localhost:6060/pkg/github.com/cmu440](http://localhost:6060/pkg/github.com/cmu440) in a browser.
Note that you can execute this command from anywhere in your system (assuming your `GOPATH`
is pointing to the project's root `p1/` directory).
