// Package skills embeds the agent skills crossmem ships. The directories here
// are the only copy: `crossmem install --skills` installs from this embed, and
// skill registries that read a repository's top-level skills/ directory find
// the same files.
package skills

import "embed"

// Loader is the name of the crossmem-loader skill directory in FS.
const Loader = "crossmem-loader"

// FS holds every bundled skill, one directory per skill.
//
//go:embed crossmem-loader
var FS embed.FS
