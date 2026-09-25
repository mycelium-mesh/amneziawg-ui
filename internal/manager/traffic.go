package manager

import (
	"time"

	"amneziawg-web-ui/internal/awg"
	"amneziawg-web-ui/web-ui/api"
)

// TrafficSnapshot collects the peer counters of every server, plus the
// host's own gauges, into the one response the page polls. A server's own
// figure is the sum over its peers, so the card and the client rows below
// it are read off the same counters and never disagree. Servers that are
// down contribute nothing; the page keeps their last counters as they were.
func (m *Manager) TrafficSnapshot() api.TrafficSnapshot {
	servers := m.copyServers()

	clientTraffic := map[string]map[string]api.ClientTraffic{}
	serverTraffic := map[string]api.InterfaceTraffic{}
	for i := range servers {
		srv := &servers[i]
		peers := m.tools.ShowPeers(srv.Interface)
		if peers == nil {
			continue
		}
		// Peers coming back is the interface being up, and this poll is
		// the most frequent one: noting it here is what keeps the uptime
		// running from the first sighting.
		m.noteServerStatus(srv.Interface, "running")
		if t := peerTraffic(srv, peers); len(t) > 0 {
			clientTraffic[srv.ID] = t
		}
		var rx, tx uint64
		for _, p := range peers {
			rx += p.RXBytes
			tx += p.TXBytes
		}
		serverTraffic[srv.ID] = api.InterfaceTraffic{
			RX: api.FormatBytes(float64(rx)), TX: api.FormatBytes(float64(tx)),
			RXBytes: rx, TXBytes: tx,
			UptimeSeconds: m.interfaceUptime(srv.Interface),
		}
	}

	return api.TrafficSnapshot{
		Timestamp:     float64(time.Now().Unix()),
		ClientTraffic: clientTraffic,
		ServerTraffic: serverTraffic,
		System:        m.host.Metrics(),
	}
}

// onlineWindow is how old a handshake may be for the peer to count as
// online. WireGuard renews the session every two minutes while traffic
// flows and drops the keys after three (REJECT_AFTER_TIME): a peer quiet for
// longer than that has no live session.
const onlineWindow = 3 * time.Minute

// peerTraffic joins what `awg show` reports with the clients the server
// owns, keyed by client ID. srv is a snapshot, so no lock is needed.
func peerTraffic(srv *api.Server, peers map[string]awg.PeerStats) map[string]api.ClientTraffic {
	result := map[string]api.ClientTraffic{}
	now := time.Now()
	for _, c := range srv.Clients {
		p, ok := peers[c.ClientPublicKey]
		if !ok {
			p = awg.PeerStats{LastHandshake: "Never"}
		}
		result[c.ID] = api.ClientTraffic{
			Received:      api.FormatBytes(float64(p.RXBytes)),
			Sent:          api.FormatBytes(float64(p.TXBytes)),
			LastHandshake: p.LastHandshake,
			Endpoint:      p.Endpoint,
			Online:        !p.HandshakeAt.IsZero() && now.Sub(p.HandshakeAt) < onlineWindow,
		}
	}
	return result
}
