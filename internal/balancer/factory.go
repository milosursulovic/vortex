package balancer

import "fmt"

// New builds the Balancer named by the load_balancing.algorithm config
// value. An empty string defaults to round-robin.
func New(algorithm string) (Balancer, error) {
	switch algorithm {
	case "", "round_robin":
		return NewRoundRobin(), nil
	case "weighted_round_robin":
		return NewWeightedRoundRobin(), nil
	case "random":
		return NewRandom(), nil
	case "least_connections":
		return NewLeastConnections(), nil
	case "power_of_two_choices":
		return NewPowerOfTwoChoices(), nil
	case "ip_hash":
		return NewIPHash(), nil
	case "consistent_hashing":
		return NewConsistentHashing(), nil
	default:
		return nil, fmt.Errorf("unknown load balancing algorithm %q", algorithm)
	}
}
