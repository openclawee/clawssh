// Package server is the SSH transport "kernel": it owns the gliderlabs/ssh listener, session
// lifecycle, and the REPL that orchestrates IntentEngine → PolicyEngine → ToolProvider.
//
// This layer should stay thin: no business rules beyond I/O, logging, and wiring. Parsing,
// policy, and execution live in sibling internal packages to preserve clean boundaries.
package server
