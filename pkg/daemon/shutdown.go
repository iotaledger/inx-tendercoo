package daemon

const (
	PriorityDisconnectINX = iota
	PriorityStopTangleListener
	PriorityStopTendermint
	PriorityStopCoordinator
)
