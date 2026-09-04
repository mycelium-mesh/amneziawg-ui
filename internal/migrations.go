package internal

// This file holds the one-shot migrations that bring a config written by an
// older release - web_config.json and the .conf files it points at - up to
// what the current code expects. They run at startup, from NewManager, before
// anything reads the config for real.
//
// The first release ships with none: schema 3 is the oldest shape there is,
// and a config that predates the field is only ever a fresh install, which
// already has that shape.
//
// To add one:
//
//   - bump awgConfigSchemaVersion to the revision the migration produces - 4
//     for the first one - and describe that revision in the list on it;
//   - give the migration its own constant holding that same revision, open
//     its body with "if m.Config.SchemaVersion >= <that constant> { return }"
//     so a later bump cannot re-run it, and call it from runMigrations.

// awgConfigSchemaVersion is the current web_config.json schema revision.
//
//	3 - the shape the first release writes: every client lives in the Clients
//	    list of its server, and every peer marker in a server .conf carries
//	    the client ID.
//
// It starts at 3 rather than 1 because pre-release builds already stamped 1
// and 2 into deployed configs. Numbering the first release below what is
// already on disk would make those files look newer than the code, and the
// next migration - guarded by "the config is older than this revision" -
// would never run on them.
const awgConfigSchemaVersion = 3

// runMigrations applies every migration the loaded config still needs and
// stamps the result, so none of them run twice.
func (m *Manager) runMigrations() {
	if m.Config.SchemaVersion >= awgConfigSchemaVersion {
		return
	}

	// Migrations go here, oldest first.

	m.Config.SchemaVersion = awgConfigSchemaVersion
	m.saveOrLog("schema migration")
}
