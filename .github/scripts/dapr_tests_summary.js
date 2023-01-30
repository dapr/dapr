
module.exports = async ({ glob, core }) => {
    const jsonFile = require(process.env["TEST_OUTPUT_FILE_PREFIX"] + "_summary_table_TestSummary.json")
    console.log(jsonFile)
    const globber = await glob.create(process.env["TEST_OUTPUT_FILE_PREFIX"] + "_summary_table_*.json")
    console.log(process.env["TEST_OUTPUT_FILE_PREFIX"] + "_summary_table_*.json")
    for await (const file of globber.globGenerator()) {
        console.log(file)
        const testSummary = require(file) // the file is a json file so we can import
        await core.summary
            .addHeading(testSummary.heading)
            .addTable(testSummary.data)
            .write()
    }
}