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
    const buildErrors = []
    const failedPackages = new Set()

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
            // gotestsum writes build errors into the stream verbatim, so a line
            // that is not an event is itself a sign the build broke.
            buildErrors.push(line.trim())
            continue
        }

        if (!event.Test) {
            if (event.Action === 'fail') {
                failedPackages.add(event.Package || 'unknown')
            }
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

    // A package always fails when one of its tests does, so its failure is only
    // news when no test accounts for it: a build error, a panic during init, or
    // a timeout. Reporting success in that case would be a lie.
    const packageErrors = buildErrors.slice()
    if (leaves.length === 0) {
        for (const pkg of failedPackages) {
            packageErrors.push(`${pkg} failed without any test failing`)
        }
    }

    if (leaves.length === 0 && packageErrors.length === 0) {
        await core.summary
            .addHeading(title, 3)
            .addRaw(`All ${total} integration tests passed.`)
            .write()
        return
    }

    // Surfaced before the per-test table: a package that did not build, or that
    // died outside a test, explains every test that never ran.
    for (const err of packageErrors) {
        core.error(err, { title: `Integration test failure (${os}/${arch})` })
    }

    for (const { test, elapsed } of leaves) {
        core.error(`${test} failed after ${elapsed.toFixed(2)}s`, {
            title: `Integration test failure: ${test}`,
        })
    }

    const summary = core.summary.addHeading(title, 3)

    if (packageErrors.length > 0) {
        summary.addRaw(
            'The package itself failed, so some tests may never have run:'
        )
        summary.addList(packageErrors)
    }

    if (leaves.length === 0) {
        await summary.write()
        return
    }

    await summary
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
