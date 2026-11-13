package store

import (
	"net/netip"

	"amneziawg-web-ui/web-ui/api"
)

// This file holds the one-shot migrations that bring a config written by an
// older release - web_config.json and the .conf files it points at - up to
// what the current code expects. They run at startup, from manager.New,
// before anything reads the config for real.
//
// To add one:
//
//   - bump SchemaVersion to the revision the migration produces and describe
//     that revision in the list on it;
//   - give the migration its own constant holding that same revision, open
//     its body with "if cfg.SchemaVersion >= <that constant> { return }" so a
//     later bump cannot re-run it, and call it from Migrate.

// SchemaVersion is the current web_config.json schema revision.
//
//	3 - the shape the first release writes: every client lives in the Clients
//	    list of its server, and every peer marker in a server .conf carries
//	    the client ID.
//	4 - every client's ServerID actually names the server it lives in; see
//	    repairClientServerIDs.
//	5 - an Endpoint holding an IP literal is the auto-detected address old
//	    releases copied in at creation; see clearAutoEndpoints.
//
// It starts at 3 rather than 1 because pre-release builds already stamped 1
// and 2 into deployed configs. Numbering the first release below what is
// already on disk would make those files look newer than the code, and the
// next migration - guarded by "the config is older than this revision" -
// would never run on them.
const SchemaVersion = 5

// Migrate applies every migration cfg still needs and stamps the result, so
// none of them run twice. It reports whether cfg changed and therefore needs
// saving; a config that is already current is left untouched.
func Migrate(cfg *AppConfig) (changed bool) {
	if cfg.SchemaVersion >= SchemaVersion {
		return false
	}

	// Migrations go here, oldest first.
	repairClientServerIDs(cfg)
	clearAutoEndpoints(cfg)

	cfg.SchemaVersion = SchemaVersion
	return true
}

const schemaClientServerIDs = 4

// repairClientServerIDs rewrites every client's ServerID from the server it is
// stored under.
//
// Releases up to 3.1.4 took that id straight from the route parameter and kept
// it, but fiber hands route parameters back as strings pointing into the
// request buffer it recycles the moment the handler returns. The id therefore
// decayed into a slice of some later request ("tatus5" off a /api/system/status
// path, "i-sett" off an .../i-settings one) and any save after that wrote the
// garbage to disk. Membership in the server's Clients list is what actually
// binds a client to its server, so the correct value is always to hand and
// nothing else needs repairing.
func repairClientServerIDs(cfg *AppConfig) {
	if cfg.SchemaVersion >= schemaClientServerIDs {
		return
	}

	for i := range cfg.Servers {
		srv := &cfg.Servers[i]
		for j := range srv.Clients {
			if srv.Clients[j].ServerID != srv.ID {
				srv.Clients[j].ServerID = srv.ID
			}
		}
	}
}

const schemaAutoEndpoint = 5

// clearAutoEndpoints clears the endpoints releases up to 3.1.9 copied into
// the fields from a detected or typed address.
//
// CreateServer wrote the endpoint into both PublicIP and Endpoint of every
// server created without an explicit one - and the endpoint it wrote was the
// detected address when the field was left empty, or the typed hostname when
// it was not. endpointFor prefers the stored Endpoint, so a server whose
// address later changed kept handing out the stale one even after "Refresh
// IP".
//
// Which stored endpoints are that copy is decided by content, not by the two
// fields being equal: equality misses a server refreshed after creation
// (PublicIP moved on, the frozen Endpoint stayed) and destroys a hostname the
// operator typed. So an Endpoint holding an IP literal is the copy - detection
// can only produce an address - and is dropped, while a hostname in Endpoint
// is the operator's and stays. A hostname that ended up in PublicIP (the old
// code copied the endpoint there too, and a typed one can be a hostname) is
// moved back to Endpoint, leaving PublicIP empty for the next detection.
func clearAutoEndpoints(cfg *AppConfig) {
	if cfg.SchemaVersion >= schemaAutoEndpoint {
		return
	}

	for i := range cfg.Servers {
		srv := &cfg.Servers[i]

		switch {
		case srv.Endpoint == api.EndpointUnknown:
			// The placeholder the app itself wrote when detection failed:
			// not a host the operator chose, so it goes and the server
			// follows the detected address again.
			srv.Endpoint = ""
		case isIPLiteral(srv.Endpoint):
			// An address in Endpoint is what detection produced - it can
			// produce nothing else - so it is the copy, and it is dropped.
			// That also frees a server left frozen on an old address by a
			// Refresh IP after the host moved.
			srv.Endpoint = ""
		}

		switch {
		case srv.PublicIP == "":
		case srv.PublicIP == api.EndpointUnknown:
			// The placeholder again: nothing was ever detected, so
			// there is nothing to keep.
			srv.PublicIP = ""
		case isIPLiteral(srv.PublicIP):
			// A detected address: it stays, and the next detection
			// refreshes it.
		default:
			// The old code copied the endpoint into PublicIP too, so a
			// hostname can be sitting there with the operator's host
			// nowhere else. Put it back where it belongs; leave PublicIP
			// empty for detection to fill.
			if srv.Endpoint == "" {
				srv.Endpoint = srv.PublicIP
			}
			srv.PublicIP = ""
		}
	}
}

// isIPLiteral reports whether s is an IPv4 or IPv6 address.
func isIPLiteral(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}
