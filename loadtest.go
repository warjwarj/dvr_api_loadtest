package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/gosuri/uilive"
	"nhooyr.io/websocket"
)

var (
	// GLOBS
	GPS_SVR           string = "192.168.1.77:9047"      // server addr
	API_SVR           string = "ws://192.168.1.77:9046" // server addr
	NUM_CLIENTS       int    = 2000                     // num of devs, num of API clients
	SAMPLE_SIZE       int    = 5000000                  // how many pings do we want.
	CONN_INTERLUDE_MS int    = 6                        // time between connection attempts in miliseconds

	// match these strings to errors
	conn_refused string = "No connection could be made because the target machine actively refused it."
	conn_aborted string = "An established connection was aborted by the software in your host machine."
)

type Aggregator struct {
	writer                    *uilive.Writer
	durTotal                  time.Duration
	pingsTotal                int
	connRefusedTotal          int
	connAbortedTotal          int
	unrecognisedErrorTotal    int
	deviceConnectionsTotal    int
	APIclientConnectionsTotal int
}

func (ag *Aggregator) Init() {
	ag.writer = uilive.New()
	ag.writer.Start()
}

func (ag *Aggregator) printTotals() {
	// printout to the screen
	fmt.Fprintf(ag.writer, "dvr_api_loadtest\n")
	fmt.Fprintf(ag.writer, "~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\n")
	fmt.Fprintf(ag.writer, "Pings: ....................................%v/%v\n", ag.pingsTotal, SAMPLE_SIZE)
	fmt.Fprintf(ag.writer, "Connections refused: ......................%v\n", ag.connRefusedTotal)
	fmt.Fprintf(ag.writer, "Connections aborted .......................%v\n", ag.connAbortedTotal)
	fmt.Fprintf(ag.writer, "Unrecognised errors .......................%v\n", ag.unrecognisedErrorTotal)
	fmt.Fprintf(ag.writer, "Successful device connections: ............%v/%v\n", ag.deviceConnectionsTotal, NUM_CLIENTS)
	fmt.Fprintf(ag.writer, "Successful API client connections: ........%v/%v\n", ag.APIclientConnectionsTotal, NUM_CLIENTS)
	fmt.Fprintf(ag.writer, "~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\n")

	// refresh the screen
	ag.writer.Flush()
}

type Set map[string]struct{}

func (s Set) Add(item string) {
	s[item] = struct{}{}
}

func (s Set) Contains(item string) bool {
	_, found := s[item]
	return found
}

func main() {

	// data aggregator
	ag := &Aggregator{}
	ag.Init()
	defer ag.writer.Stop()

	uniqueErrors := make(Set)

	// setup channels for data intake
	durations := make(chan time.Duration, SAMPLE_SIZE)
	errors := make(chan error)
	deviceConnections := make(chan bool, NUM_CLIENTS)
	deviceDisconnections := make(chan bool, NUM_CLIENTS)
	APIclientConnections := make(chan bool, NUM_CLIENTS)
	APIclientDisconnections := make(chan bool, NUM_CLIENTS)

	// cancel when we have enough data
	ctx, cancel := context.WithCancel(context.Background())

	// background so we see the devices connect in real time
	go func() {
		// create (numClients) mock devices & (numClients) mock API clients
		for i := 0; i < NUM_CLIENTS; i++ {
			// gen rand dev id
			devId := rand.Intn(9000000000) + 1000000000

			// create our devices
			go mockDevice(ctx, devId, errors, deviceConnections, deviceDisconnections)
			go mockAPIClient(ctx, devId, errors, APIclientConnections, APIclientDisconnections, durations)

			// wait x amount of miliseconds
			time.Sleep(time.Millisecond * time.Duration(CONN_INTERLUDE_MS))
		}
	}()

	// label so we can break out of the loop
AggregateData:
	// loop over channels
	for ag.pingsTotal != SAMPLE_SIZE {
		select {
		// record ping times
		case dur, ok := <-durations:
			if ok {
				ag.durTotal += dur
				ag.pingsTotal++
			} else {
				fmt.Println("Fatal error receiving from durations chan, ceasing data aggregation")
				break AggregateData
			}
		// record errors
		case err, ok := <-errors:
			if !uniqueErrors.Contains(err.Error()) {
				uniqueErrors.Add(err.Error())
			}
			if ok {
				if strings.Contains(err.Error(), conn_refused) {
					ag.connRefusedTotal++
				} else if strings.Contains(err.Error(), conn_aborted) {
					ag.connAbortedTotal++
				} else {
					ag.unrecognisedErrorTotal++
				}
			} else {
				fmt.Println("Fatal error receiving from error channel, returning")
				break AggregateData
			}
		// record successful conections devices
		case _, ok := <-deviceConnections:
			if ok {
				ag.deviceConnectionsTotal++
			} else {
				fmt.Println("Fatal error receiving from dev connection counter chan, returning")
				break AggregateData
			}
		// record successful conections devices
		case _, ok := <-deviceDisconnections:
			if ok {
				ag.deviceConnectionsTotal--
			} else {
				fmt.Println("Fatal error receiving from dev connection counter chan, returning")
				break AggregateData
			}
		// record successful connections API client
		case _, ok := <-APIclientConnections:
			if ok {
				ag.APIclientConnectionsTotal++
			} else {
				fmt.Println("Fatal error receiving from API client connection counter chan, returning")
				break AggregateData
			}
		// record successful connections API client
		case _, ok := <-APIclientDisconnections:
			if ok {
				ag.APIclientConnectionsTotal--
			} else {
				fmt.Println("Fatal error receiving from API client connection counter chan, returning")
				break AggregateData
			}
		}
		ag.printTotals()
	}

	// cancel the context
	cancel()

	// update the console
	ag.printTotals()

	// mean server ping
	meanPing := ag.durTotal / time.Duration(cap(durations))
	fmt.Println("Mean server ping: ", meanPing)

	// // print unique errors
	// fmt.Println("Unique errors:")
	// for i := range uniqueErrors {
	// 	fmt.Println(i)
	// }
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
		defer conn.Close()

		message := fmt.Sprintf("$ALV;%d;Hello Server!\r", devId)
		_, err = conn.Write([]byte(message))
		if err != nil {
			errChan <- err
			deviceDisconnections <- true
			return
		}
		buf := make([]byte, 256)
		for {
			// just echo recvd back
			_, err := conn.Read(buf)
			if err != nil {
				errChan <- err
				deviceDisconnections <- true
				return
			}
			_, err = conn.Write(buf)
			if err != nil {
				errChan <- err
				deviceDisconnections <- true
				return
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
			time.Sleep(time.Millisecond * 10) // Adjust the interval between messages as needed
		}
	}
}
