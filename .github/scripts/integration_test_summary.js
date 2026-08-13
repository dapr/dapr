const fs = require('fs')
const readline = require('readline')

// Reads the `go test -json` event stream written by gotestsum --jsonfile and
// reports the failed tests as a job summary table plus one annotation each, so
// that failures are visible without opening the raw log.
module.exports = async ({ core }) => {
    const file =
        process.env['TEST_OUTPUT_FILE_PREFIX'] + '_integration.json'

    if (!fs.existsSync(file)) {
        core.warning(`No integration test report found at ${file}`)
        return
    }

    const failures = []
    const finished = new Set()

    const stream = readline.createInterface({
        input: fs.createReadStream(file),
        crlfDelay: Infinity,
    })

    for await (const line of stream) {
        if (!line.trim()) {
            continue
        }

        let event
        try {
            event = JSON.parse(line)
        } catch {
            // gotestsum writes build errors into the stream verbatim.
            continue
        }

        if (!event.Test) {
            continue
        }
        if (event.Action === 'pass' || event.Action === 'fail') {
            finished.add(event.Test)
        }
        if (event.Action === 'fail') {
            failures.push({
                test: event.Test,
                elapsed: event.Elapsed ?? 0,
            })
        }
    }

    // A parent test fails whenever a subtest does, and every case is a subtest
    // of Test_Integration. Counting and reporting only the leaves keeps the
    // table to the tests that actually broke.
    const parents = new Set(
        [...finished].flatMap((test) => {
            const parts = test.split('/')
            return parts
                .slice(0, -1)
                .map((_, i) => parts.slice(0, i + 1).join('/'))
        })
    )
    const total = [...finished].filter((test) => !parents.has(test)).length
    const leaves = failures.filter(({ test }) => !parents.has(test))

    const os = process.env['GOOS'] || process.platform
    const arch = process.env['GOARCH'] || process.arch
    const title = `Integration tests (${os}/${arch})`

    if (leaves.length === 0) {
        await core.summary
            .addHeading(title, 3)
            .addRaw(`All ${total} integration tests passed.`)
            .write()
        return
    }

    for (const { test, elapsed } of leaves) {
        core.error(`${test} failed after ${elapsed.toFixed(2)}s`, {
            title: `Integration test failure: ${test}`,
        })
    }

    await core.summary
        .addHeading(title, 3)
        .addRaw(`${leaves.length} of ${total} integration tests failed.`)
        .addTable([
            [
                { data: 'Test', header: true },
                { data: 'Duration', header: true },
            ],
            ...leaves.map(({ test, elapsed }) => [
                test,
                `${elapsed.toFixed(2)}s`,
            ]),
        ])
        .write()
}
