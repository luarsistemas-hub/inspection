package core

// CanTransition reports whether a notification state change can advance the
// durable lifecycle. Terminal states never regress or restart implicitly.
func CanTransition(from, to State) bool {
	if from == to {
		return true
	}
	switch from {
	case StateQueued:
		return to == StateProcessing || to == StateCanceled
	case StateProcessing:
		return to == StateQueued || to == StateAccepted || to == StateSent || to == StateDelivered || to == StateFailed || to == StateUnknown || to == StateCanceled
	case StateAccepted:
		return to == StateSent || to == StateDelivered || to == StateFailed || to == StateUnknown || to == StateCanceled
	case StateSent:
		return to == StateDelivered || to == StateFailed || to == StateUnknown || to == StateCanceled
	default:
		return false
	}
}

// NormalizeProviderStatus maps provider callback vocabulary into durable
// states. The second result is false for an unsupported status.
func NormalizeProviderStatus(status string) (State, bool) {
	switch status {
	case "accepted", "queued":
		return StateAccepted, true
	case "sending", "sent":
		return StateSent, true
	case "delivered", "read":
		return StateDelivered, true
	case "failed", "undelivered", "rejected":
		return StateFailed, true
	case "unknown", "expired":
		return StateUnknown, true
	default:
		return "", false
	}
}
