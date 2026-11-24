//go:build coreengine

package tests

// coreEngineTag reports the build with the tag coreengine, in which every
// rule of rules/core runs on the engine instead of the table of a pure core
// rule. The normal build runs the table.
const coreEngineTag = true
