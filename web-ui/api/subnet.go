package api

import (
	"fmt"
	"net"
)

// MaxSubnetPrefix is the longest prefix a server's subnet may have: a /30 is
// the smallest block with room for the server and one client between its
// network and broadcast addresses.
const MaxSubnetPrefix = 30

// ParseSubnet reads a server's subnet: an IPv4 CIDR with room for at least
// one client. The result is the network itself, so "10.0.0.5/24" comes back
// as 10.0.0.0/24.
func ParseSubnet(subnet string) (*net.IPNet, error) {
	ip, network, err := net.ParseCIDR(subnet)
	if err != nil || ip.To4() == nil {
		return nil, fmt.Errorf("subnet %q is not an IPv4 CIDR such as 10.0.0.0/24", subnet)
	}
	if ones, _ := network.Mask.Size(); ones > MaxSubnetPrefix {
		return nil, fmt.Errorf("subnet %q is too small: the prefix can be at most /%d", subnet, MaxSubnetPrefix)
	}
	return network, nil
}

// SubnetsOverlap reports whether two subnets share any address. Two servers
// on overlapping subnets would route the same client addresses to two
// interfaces, so one of them silently stops receiving traffic. A subnet that
// does not parse overlaps nothing; ParseSubnet is what reports it.
func SubnetsOverlap(a, b string) bool {
	na, errA := ParseSubnet(a)
	nb, errB := ParseSubnet(b)
	if errA != nil || errB != nil {
		return false
	}
	return na.Contains(nb.IP) || nb.Contains(na.IP)
}
