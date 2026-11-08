// Package hoststate names the states a host can be in.
package hoststate

// State is a host's condition as a person would describe it.
type State string

const (
	Ready    State = "ready"
	NotReady State = "notready"
	// Asleep is suspended on purpose, e.g. by a power manager; its Node object is kept.
	Asleep State = "asleep"
	// Off is powered off on purpose; a Kubernetes node has no Node object.
	Off State = "off"
	// Booting is being powered on.
	Booting State = "booting"
	// Maintenance is down or drained on purpose: cordoned, or marked by a person.
	Maintenance State = "maintenance"
	// Unreachable sent metrics recently but has stopped.
	Unreachable State = "unreachable"
)
