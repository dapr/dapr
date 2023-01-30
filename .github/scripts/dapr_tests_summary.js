
module.exports = async ({ glob, core }) => {
    const globber = await glob.create(process.env["TEST_OUTPUT_FILE_PREFIX"] + "_summary_table_*.json")
    for await (const file of globber.globGenerator()) {
        const testSummary = require(file) // the file is a json file so we can import
        await core.summary
            .addHeading(testSummary.test)
            .addTable(testSummary.data)
            .write()
    }
}