package main

import (
	"sync"
	"time"
)

// Processes control the
type Processes struct {
	client        *Client
	config        *Config
	reserveLock   sync.Locker
	reserved      map[string]reservation
	nextReserve   map[string]reservation
	ticker        *time.Ticker
	routineTicker chan struct{}
	wg            *sync.WaitGroup
	closeFuncs    map[string]func()
}

// reservation holds the reserved API info calls.
type reservation struct {
	reservation uint64
	unused      uint64
	buffer      uint64
}

// NewProcesses creates a structure for managing processes.
func NewProcesses(client *Client, config *Config) *Processes {
	var reserveLock *sync.Mutex
	reserved := make(map[string]reservation)
	nextReserve := make(map[string]reservation)
	ticker := time.NewTicker(config.Application.TickSpeed)
	routineTicker := make(chan struct{})
	wg := &sync.WaitGroup{}
	closeFuncs := make(map[string]func())

	return &Processes{client, config, reserveLock, reserved, nextReserve, ticker, routineTicker, wg, closeFuncs}
}

// Start begins the process loop.
func (p *Processes) Start() {
	c := p.client
	timer := time.NewTicker(c.Config.Application.LoopDelay)
	go func() {
		for !c.closed {
			p.routineTicker <- struct{}{}
			p.wg.Wait()

			// The timer can be cancelled using this method, but should otherwise be equivalent to time.Sleep(...)
			timer.Reset(c.Config.Application.LoopDelay)
			<-timer.C

			for !c.closed && !c.Reddit.IsRateLimited() {
				<-p.Tick()
			}
		}
	}()

	for !c.closed {
		<-p.Tick()
	}

	p.ticker.Stop()
	<-p.Tick()

	timer.Reset(0)
	<-timer.C

	<-p.routineTicker
	close(p.routineTicker)
}

// OnClose adds a function called when a process closes.
func (p *Processes) OnClose(processName string, closeFunc func()) {
	p.closeFuncs[processName] = closeFunc
}

// Close closes the process loop.
func (p *Processes) Close() {
	p.client.closed = true
	<-p.routineTicker
	close(p.routineTicker)

	for _, closeFunc := range p.closeFuncs {
		closeFunc()
	}
}

// RoutineStart will begin a routine.
func (p *Processes) RoutineStart(name string) {

}

// RoutineWait will block until the process may continue.
func (p *Processes) RoutineWait(name string) {
	<-p.routineTicker
}

// RoutineCrash will handle a routine crashing.
func (p *Processes) RoutineCrash(name string) {
	if err := recover(); err != nil {
		p.client.dpanic(err)
	}
}

// ReserveNextLoop is.
func (p *Processes) ReserveNextLoop(name string, reservation uint64, buffer uint64) {}

// Release will release any held limits
func (p *Processes) Release(name string) {}

// Tick returns the ticker for the tick speed.
func (p *Processes) Tick() <-chan time.Time {
	return p.ticker.C
}
