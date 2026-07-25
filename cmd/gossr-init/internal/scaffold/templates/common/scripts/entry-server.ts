// gossr consumes a standalone script through the global render ABI and has no
// CommonJS `exports` object. Keep the Rollup entry side-effect-only so the
// application entry can install that ABI without emitting an exports shim.
import '../src/entry-server'
