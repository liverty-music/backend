package messaging

// NewConsumerHealthWithClock exposes newConsumerHealth for testing with a fake
// clock.
var NewConsumerHealthWithClock = newConsumerHealth

// ConnectWithRetry exposes connectWithRetry for testing with a short budget.
var ConnectWithRetry = connectWithRetry

// NATSReconnectDelay exposes natsReconnectDelay for testing.
var NATSReconnectDelay = natsReconnectDelay

// WarnOnDisconnect exposes warnOnDisconnect for testing.
var WarnOnDisconnect = warnOnDisconnect
