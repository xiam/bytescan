//go:build !coreengine

package core

// coreEngineTag is the default of engineForced: false in the normal build,
// so a pure core rule runs on its table.
const coreEngineTag = false
