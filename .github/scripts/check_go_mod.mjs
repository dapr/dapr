#!/usr/bin/env node

import { readFile } from 'node:fs/promises'

const match = `// Uncomment for local development for testing with changes in the components-contrib && kit repositories.
// Don't commit with this uncommented!
//
// replace github.com/dapr/components-contrib => ../components-contrib
// replace github.com/dapr/kit => ../kit
//
// Then, run \`make modtidy-all\` in this repository.
// This ensures that go.mod and go.sum are up-to-date for each go.mod file.`

const read = await readFile('go.mod', { encoding: 'utf8' })
if (!read.includes(match)) {
    console.log(
        'File go.mod was committed with a change in the block around "replace github.com/dapr/components-contrib"'
    )
    process.exit(1)
}
console.log('go.mod is ok')
