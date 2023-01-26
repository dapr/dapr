#!/usr/bin/env node

import {readFile} from 'node:fs/promises'

const match = `// Uncomment for local development for testing with changes in the components-contrib repository.
// Don't commit with this uncommented!
//
// replace github.com/dapr/components-contrib => ../components-contrib
//
// Then, run \`make modtidy\` in this repository.
// This ensures that go.mod and go.sum are up-to-date.`

const read = await readFile('go.mod', {encoding: 'utf8'})
if (!read.includes(match)) {
    console.log('File go.mod was committed with a change in the block around "replace github.com/dapr/components-contrib"')
    process.exit(1)
}
console.log('go.mod is ok')
