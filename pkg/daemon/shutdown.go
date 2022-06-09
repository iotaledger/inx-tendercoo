package daemon

// shutdown order
const (
	PriorityStopTangleListener = iota
	PriorityStopTendermint
	PriorityStopCoordinator
)
