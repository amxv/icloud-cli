//go:build race

package app

// Race instrumentation can make SQLite-heavy package runs orders of magnitude
// slower than production execution. Keep explicit timeout tests unchanged while
// preventing the implicit 30-second CLI default from becoming a test-harness
// failure after hundreds of instrumented commands have run in one process.
const raceTestCommandTimeout = "5m"
