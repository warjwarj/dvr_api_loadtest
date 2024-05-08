package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/gosuri/uilive"
	"nhooyr.io/websocket"
)

const (
	// GLOBS
	GPS_SVR           string = "192.168.1.127:9047"      // server addr
	API_SVR           string = "ws://192.168.1.127:9046" // server addr
	NUM_CLIENTS       int    = 10000                     // num of devs, num of API clients
	SAMPLE_SIZE       int    = 5000000                   // how many pings to calc mean.
	CONN_INTERLUDE_MS int    = 50                        // time between connection attempts in miliseconds
	MSG_INTERLUDE_MS  int    = 10                        // time between pings in miliseconds
)

type Aggregator struct {
	// write to the console
	writer *uilive.Writer

	// tally
	durTotal               time.Duration
	pingsTotal             int
	connRefusedTotal       int
	connResetTotal         int
	connAbortedTotal       int
	unrecognisedErrorTotal int
	deviceConnTotal        int
	APIclientConnTotal     int

	// chans we receive data from
	pingVals       chan time.Duration
	connErrors     chan error
	devConns       chan bool
	devDisconns    chan bool
	APIcliConns    chan bool
	APIcliDisconns chan bool
}

func (ag *Aggregator) Init() {
	// init writer
	ag.writer = uilive.New()
	ag.writer.Start()

	// init chans
	ag.pingVals = make(chan time.Duration, SAMPLE_SIZE)
	ag.connErrors = make(chan error, NUM_CLIENTS*2)
	ag.devConns = make(chan bool, NUM_CLIENTS)
	ag.devDisconns = make(chan bool, NUM_CLIENTS)
	ag.APIcliConns = make(chan bool, NUM_CLIENTS)
	ag.APIcliDisconns = make(chan bool, NUM_CLIENTS)
}

func (ag *Aggregator) printTotals() {
	// printout to the screen
	fmt.Fprintf(ag.writer, "\ndvr_api_loadtest\n")
	fmt.Fprintf(ag.writer, "~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\n")
	fmt.Fprintf(ag.writer, "Pings: ....................................%v/%v\n", ag.pingsTotal, SAMPLE_SIZE)
	fmt.Fprintf(ag.writer, "Connections refused: ......................%v\n", ag.connRefusedTotal)
	fmt.Fprintf(ag.writer, "Connections reset: ........................%v\n", ag.connResetTotal)
	fmt.Fprintf(ag.writer, "Connections aborted .......................%v\n", ag.connAbortedTotal)
	fmt.Fprintf(ag.writer, "Unrecognised errors .......................%v\n", ag.unrecognisedErrorTotal)
	fmt.Fprintf(ag.writer, "Successful device connections: ............%v/%v\n", ag.deviceConnTotal, NUM_CLIENTS)
	fmt.Fprintf(ag.writer, "Successful API client connections: ........%v/%v\n", ag.APIclientConnTotal, NUM_CLIENTS)
	fmt.Fprintf(ag.writer, "~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~")

	// refresh the screen
	ag.writer.Flush()
}

func main() {
	// data aggregator
	ag := &Aggregator{}
	ag.Init()
	defer ag.writer.Stop()

	// cancel when we have enough data
	ctx, cancel := context.WithCancel(context.Background())

	// connect the devices in the background
	go func() {
		// create (numClients) mock devices & (numClients) mock API clients
		for i := 0; i < NUM_CLIENTS; i++ {
			// not rand in case of duplicates
			devId := 1000000000 + i*i

			// create our devices
			go mockDevice(ctx, devId, ag.connErrors, ag.devConns, ag.devDisconns)
			go mockAPIClient(ctx, devId, ag.connErrors, ag.APIcliConns, ag.APIcliDisconns, ag.pingVals)

			// wait x amount of miliseconds
			time.Sleep(time.Millisecond * time.Duration(CONN_INTERLUDE_MS))
		}
	}()

	// label so we can break out of the loop
	// loop over channels, selecting ones which have data available
AggregateData:
	for ag.pingsTotal != SAMPLE_SIZE {
		select {
		// record ping times
		case dur, ok := <-ag.pingVals:
			if ok {
				ag.durTotal += dur
				ag.pingsTotal++
			} else {
				fmt.Println("Fatal error receiving from durations chan, ceasing data aggregation")
				break AggregateData
			}
		// record errors
		case err, ok := <-ag.connErrors:
			if ok {
				if errors.Is(err, syscall.ECONNREFUSED) {
					ag.connRefusedTotal++
				} else if errors.Is(err, syscall.ECONNRESET) {
					ag.connResetTotal++
				} else if errors.Is(err, syscall.ECONNABORTED) {
					ag.connAbortedTotal++
				} else {
					ag.unrecognisedErrorTotal++
				}
			} else {
				fmt.Println("Fatal error receiving from error channel, returning")
				break AggregateData
			}
		// record successful conections devices
		case _, ok := <-ag.devConns:
			if ok {
				ag.deviceConnTotal++
			} else {
				fmt.Println("Fatal error receiving from dev connection counter chan, returning")
				break AggregateData
			}
		// record successful conections devices
		case _, ok := <-ag.devDisconns:
			if ok {
				ag.deviceConnTotal--
			} else {
				fmt.Println("Fatal error receiving from dev connection counter chan, returning")
				break AggregateData
			}
		// record successful connections API client
		case _, ok := <-ag.APIcliConns:
			if ok {
				ag.APIclientConnTotal++
			} else {
				fmt.Println("Fatal error receiving from API client connection counter chan, returning")
				break AggregateData
			}
		// record successful connections API client
		case _, ok := <-ag.APIcliDisconns:
			if ok {
				ag.APIclientConnTotal--
			} else {
				fmt.Println("Fatal error receiving from API client connection counter chan, returning")
				break AggregateData
			}
		}
		ag.printTotals()
	}

	// cancel the context
	cancel()

	// print mean server ping
	meanPing := ag.durTotal / time.Duration(cap(ag.pingVals))
	fmt.Printf("\nMean server ping ---> %v\n", meanPing)
}

func mockDevice(
	ctx context.Context,
	devId int,
	errChan chan<- error,
	deviceConnections chan<- bool,
	deviceDisconnections chan<- bool,
) {
	for {
		// wait in case of reconnect
		time.Sleep(time.Millisecond * time.Duration(CONN_INTERLUDE_MS))
		conn, err := net.Dial("tcp", GPS_SVR)
		deviceConnections <- true
		if err != nil {
			errChan <- err
			deviceDisconnections <- true
			continue
		}
		defer func() {
			deviceDisconnections <- true
			conn.Close()
		}()

		message := fmt.Sprintf("$ALV;%d;Hello Server!\r", devId)
		_, err = conn.Write([]byte(message))
		if err != nil {
			errChan <- err
			deviceDisconnections <- true
			break
		}
		buf := make([]byte, 256)
		for {
			// just echo recvd back
			_, err := conn.Read(buf)
			if err != nil {
				errChan <- err
				deviceDisconnections <- true
				break
			}
			_, err = conn.Write(buf)
			if err != nil {
				errChan <- err
				deviceDisconnections <- true
				break
			}
		}
	}
}

func mockAPIClient(
	ctx context.Context,
	targetDevice int,
	errChan chan<- error,
	APIClientConnections chan<- bool,
	APIClientDisconnections chan<- bool,
	durationsChan chan<- time.Duration,
) {
	for {
		// connect to server, wait in case of reconnect
		time.Sleep(time.Millisecond * time.Duration(CONN_INTERLUDE_MS))
		conn, _, err := websocket.Dial(ctx, API_SVR, nil)
		APIClientConnections <- true
		if err != nil {
			// record the error and the disconnection.
			errChan <- err
			APIClientDisconnections <- true
			continue
		}
		defer func() {
			//fmt.Println("Closing API client connection")
			APIClientDisconnections <- true
			conn.Close(websocket.StatusNormalClosure, "")
		}()

		// send + recv messages and calc ping
		for {
			// send a ping, recording time before we sent it
			before := time.Now()
			message := fmt.Sprintf("$MOCKCMD!;%v;sampletext\r", targetDevice)
			err := conn.Write(ctx, websocket.MessageText, []byte(message))
			if err != nil {
				errChan <- err
				APIClientDisconnections <- true
				break
			}

			// read ping response and calculate time diff
			_, _, err = conn.Read(ctx)
			if err != nil {
				errChan <- err
				APIClientDisconnections <- true
				break
			}
			after := time.Now()
			difference := after.Sub(before)

			// record ping and wait
			durationsChan <- difference
			time.Sleep(time.Millisecond * time.Duration(MSG_INTERLUDE_MS)) // Adjust the interval between messages as needed
		}
	}
}
