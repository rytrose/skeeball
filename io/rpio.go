package io

import (
	"fmt"
	"time"

	"github.com/stianeikeland/go-rpio/v4"
)

// RPIOClient is the RPIO singleton.
var RPIOClient *rPIO

// DefaultPollFreq is the default pin polling frequency.
const DefaultPollFreq = 100 * time.Millisecond

func init() {
	// Instatiate RPIO client singleton
	RPIOClient = &rPIO{
		open:    false,
		polling: false,
		poller: &rpioPoller{
			ticker:         time.NewTicker(DefaultPollFreq),
			registeredPins: make(map[rpio.Pin]pinRegistration),
			newPin:         make(chan pinRegistration),
			removePin:      make(chan rpio.Pin),
			newPollFreq:    make(chan time.Duration),
			stop:           make(chan struct{}),
		},
		registeredPins: make(map[rpio.Pin]bool),
	}
}

// rPIO is a wrapper interfacing with Raspberry Pi GPIO.
type rPIO struct {
	open           bool              // open maintains state of GPIO.
	polling        bool              // polling maintains state of polling.
	poller         *rpioPoller       // poller manages polling pins for edge detection.
	registeredPins map[rpio.Pin]bool // registeredPins keeps track of what pins are registered.
}

// Start opens the GPIO pins and starts polling.
func (r *rPIO) Start() {
	if r.open {
		// Only attempt to open once
		return
	}

	// Open GPIO
	err := rpio.Open()
	if err != nil {
		panic(fmt.Sprintf("unable to open GPIO: %s", err))
	}

	r.open = true
}

// Poll scans pin states and exercises callbacks when registered pin events are detected.
func (r *rPIO) Poll() {
	if r.polling {
		// Only poll once
		return
	}

	// Start polling
	go r.poller.poll()

	r.polling = true
}

// StopPolling stops scanning pins for pin events.
func (r *rPIO) StopPolling() {
	if !r.polling {
		// Don't attempt to stop polling if not started
		return
	}

	// Signal polling goroutine to stop
	r.poller.stop <- struct{}{}

	r.polling = false
}

// Stop closes GPIO and stops polling.
func (r *rPIO) Stop() {
	if !r.open {
		// Don't attempt to stop if not started
		return
	}

	// Stop polling
	if r.polling {
		r.StopPolling()
	}

	// Close GPIO
	err := rpio.Close()
	if err != nil {
		panic(fmt.Sprintf("unable to close GPIO: %s", err))
	}

	r.open = false
}

// RegisterEdgeDetection registers a callback for a detected edge on a specified pin.
// Requires rPIO.Poll() to be called in order to detect events.
func (r *rPIO) RegisterEdgeDetection(pin rpio.Pin, edge rpio.Edge, callback func(rpio.Edge)) error {
	if !r.open {
		return fmt.Errorf("GPIO is not yet open")
	}

	if !r.polling {
		return fmt.Errorf("not yet polling GPIO")
	}

	_, exists := r.registeredPins[pin]
	if exists {
		return fmt.Errorf("pin is already registered, call RemoveEdgeDetectionRegistration before attempting a new registration")
	}

	// Only one registration per pin
	r.registeredPins[pin] = true

	// Setup detection
	pin.Detect(edge)

	// Register with poller
	r.poller.newPin <- pinRegistration{
		pin:      pin,
		edge:     edge,
		callback: callback,
	}

	return nil
}

// RemoveEdgeDetectionRegistration removes an edge detection registration for a specified pin.
func (r *rPIO) RemoveEdgeDetectionRegistration(pin rpio.Pin) error {
	if !r.open {
		return fmt.Errorf("GPIO is not yet open")
	}

	if !r.polling {
		return fmt.Errorf("not yet polling GPIO")
	}

	_, exists := r.registeredPins[pin]
	if !exists {
		return fmt.Errorf("pin is not yet registered")
	}

	// Remove pin registration
	delete(r.registeredPins, pin)

	// Clear detection
	pin.Detect(rpio.NoEdge)

	// Remove registration with poller
	r.poller.removePin <- pin

	return nil
}

// UpdatePollFreq changes the polling frequency of edge detection.
func (r *rPIO) UpdatePollFreq(d time.Duration) error {
	if !r.open {
		return fmt.Errorf("polling has not yet started")
	}

	// Update the poller frequency
	r.poller.newPollFreq <- d

	return nil
}

// pinRegistration is a registration for a callback when an edge is detected for a pin.
type pinRegistration struct {
	pin      rpio.Pin        // pin is the pin to monitor for edge detection.
	edge     rpio.Edge       // edge is the type of edge to run the callback on.
	callback func(rpio.Edge) // callback is the function to run when an edge is detected.
}

// rpioPoller manages polling pins for edge detection.
type rpioPoller struct {
	ticker         *time.Ticker                 // ticker manages the polling period.
	registeredPins map[rpio.Pin]pinRegistration // registeredPins contains which pins should be polled for what edge detection.
	newPin         chan pinRegistration         // newPins allows a new pin to be incorporated into polling.
	removePin      chan rpio.Pin                // removePin allows a pin to be removed from polling.
	newPollFreq    chan time.Duration           // newPollFreq updates the polling frequency.
	stop           chan struct{}                // stop ends polling.
}

// poll starts the pin polling routine.
func (p *rpioPoller) poll() {
pollLoop:
	for {
		select {
		case <-p.ticker.C:
			// Read pins and handle edge detection
			for pin, registration := range p.registeredPins {
				if pin.EdgeDetected() {
					go registration.callback(registration.edge)
				}
			}
		case newRegistration := <-p.newPin:
			// Add pin registration to pins to poll
			p.registeredPins[newRegistration.pin] = newRegistration
		case registrationToRemove := <-p.removePin:
			// Remove pin registration from pins to poll
			delete(p.registeredPins, registrationToRemove)
		case newPollFreq := <-p.newPollFreq:
			// Update the ticker polling frequency
			p.ticker.Reset(newPollFreq)
		case <-p.stop:
			break pollLoop
		}
	}
}
