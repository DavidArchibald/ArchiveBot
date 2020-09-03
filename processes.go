package main

import (
	"sync"
	"time"
)

// Processes control the
type Processes struct {
	reserveLock sync.Locker
	reserved    map[string]reservation
	nextReserve map[string]reservation
	ticker      chan struct{}
	wg          *sync.WaitGroup
}

// reservation holds the reserved API info calls.
type reservation struct {
	reservation uint64
	unused      uint64
	buffer      uint64
}

// NewProcesses creates a structue for managing
func NewProcesses() *Processes {
	var reserveLock *sync.Mutex
	var reserved = make(map[string]reservation)
	var nextReserve = make(map[string]reservation)
	var ticker = make(chan struct{})
	wg := &sync.WaitGroup{}

	return &Processes{reserveLock, reserved, nextReserve, ticker, wg}
}

// StartProcesses begins the process loop.
func (c *Client) StartProcesses() {
	p := c.Processes
	timer := time.NewTicker(c.Config.Application.LoopDelay)
	go func() {
		for !c.closed {
			p.ticker <- struct{}{}
			p.wg.Wait()

			// The timer can be cancelled using this method, but should otherwise be equivalent to time.Sleep(...)
			timer.Reset(c.Config.Application.LoopDelay)
			<-timer.C
		}
	}()

	checker := time.NewTicker(time.Second)
	for !c.closed {
		<-checker.C
	}

	checker.Stop()
	<-checker.C

	timer.Reset(0)
	<-timer.C

	<-p.ticker
	close(p.ticker)
}

// CloseProcesses closes the process loop.
func (c *Client) CloseProcesses() {
	c.closed = true
	<-c.Processes.ticker
	close(c.Processes.ticker)
}

// RoutineStart will begin a routine.
func (c *Client) RoutineStart(name string) {

}

// RoutineWait will block until the process may continue.
func (c *Client) RoutineWait(name string) {
	<-c.Processes.ticker
}

// RoutineCrash will handle a routine crashing.
func (c *Client) RoutineCrash(name string) {
	if err := recover(); err != nil {
		c.dpanic(err)
	}
}

// ReserveNextLoop is.
func (c *Client) ReserveNextLoop(name string, reservation uint64, buffer uint64) {}

// Release will release any held limits
func (c *Client) Release(name string) {}
