//go:build coreengine

package core

// coreEngineTag is the default of engineForced: true in the build with the
// tag coreengine, a build for the tests only, in which every core rule runs
// on the engine.
const coreEngineTag = true
