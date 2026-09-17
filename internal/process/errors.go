package process

import "errors"

// ErrLaunchInFlight is returned when a lifecycle operation (Stop, Restart)
// targets an instance whose launch is still in flight (state pending: slot
// acquisition or spawn not yet complete), or when a duplicate manual Start
// is rejected because an in-flight instance of the same model exists.
// HTTP handlers map it to 409 with code launch_in_flight.
var ErrLaunchInFlight = errors.New("launch in flight: instance launch is not complete")
