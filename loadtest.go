package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"time"

	"nhooyr.io/websocket"
)

var (
	GPSSvr     string = "192.168.1.127:9047"  // server addr
	APISvr     string = "ws://127.0.0.1:9046" // server addr
	numClients int    = 10                    // num of devs, num of API clients
	sampleSize int    = 500                   // how many pings do we want.
)

func main() {

	// setup
	durations := make(chan time.Duration, sampleSize)
	//durationsLog := []time.Duration{}

	// cancel when we have enough data
	ctx, cancel := context.WithCancel(context.Background())

	// create (numClients) mock devices & (numClients) mock API clients
	for i := 0; i < numClients; i++ {
		devId := rand.Intn(9000000000) + 1000000000
		go mockDevice(ctx, devId)
		go mockAPIClient(ctx, devId, durations)
	}

	// get enough datapoints
	var durTotal time.Duration
AggregateData:
	for range cap(durations) {
		// use a select in case we want to add more channels
		select {
		case val, ok := <-durations:
			if ok {
				durTotal += val
			} else {
				fmt.Println("Err receiving from durations chan, ceasing data aggregation")
				break AggregateData
			}
		}
		fmt.Println()
	}
	cancel()
	meanPing := durTotal / time.Duration(cap(durations))
	fmt.Println("Mean server ping: ", meanPing)
}

func mockDevice(ctx context.Context, devId int) {
	conn, err := net.Dial("tcp", GPSSvr)
	if err != nil {
		fmt.Printf("Simulated device %d: Error connecting to server: %v\n", devId, err)
		return
	}
	defer conn.Close()

	fmt.Printf("Simulated device %d: Connected to server\n", devId)
	message := fmt.Sprintf("$ALV;%d;Hello Server!\r", devId)
	_, err = conn.Write([]byte(message))
	if err != nil {
		fmt.Printf("Simulated device %d: Error sending message: %v\n", devId, err)
		return
	}
	buf := make([]byte, 256)
	for {
		// just echo recvd back
		_, err := conn.Read(buf)
		if err != nil {
			fmt.Println("Err reading from connection in simulated device connection loop")
			return
		}
		_, err = conn.Write(buf)
		if err != nil {
			fmt.Println("Err writing to connection in simulated device connection loop")
			return
		}
	}
}

func mockAPIClient(ctx context.Context, targetDevice int, durations chan<- time.Duration) {
	// connect to server
	conn, _, err := websocket.Dial(ctx, APISvr, nil)
	if err != nil {
		fmt.Printf("Error connecting mock API client to server: %v\n", err)
		return
	}
	defer func() {
		fmt.Println("Closing API client connection")
		conn.Close(websocket.StatusNormalClosure, "")
	}()
	if err != nil {
		fmt.Println("Error setting read deadline: ", err)
	}
	fmt.Printf("API client connected to server\n")

	// send + recv messages and calc ping
	for {
		// send a ping, recording time before we sent it
		before := time.Now()
		message := fmt.Sprintf("$MOCKCMD!;%v;sampletext\r", targetDevice)
		err := conn.Write(ctx, websocket.MessageText, []byte(message))
		if err != nil {
			fmt.Printf("Err writing to connection in API client connection loop", err)
			return
		}

		// read ping response and calculate time diff
		_, _, _ = conn.Read(ctx)
		after := time.Now()
		difference := after.Sub(before)

		// try insert diff value into chan
		durations <- difference

		time.Sleep(time.Millisecond * 10) // Adjust the interval between messages as needed
	}
}
