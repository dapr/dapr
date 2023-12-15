// List of owner who can control dapr-bot workflow
// IMPORTANT: Make sure usernames are lower-cased
// TODO: Read owners from OWNERS file.
const owners = [
    'addjuarez',
    'artursouza',
    'berndverst',
    'daixiang0',
    'deepanshua',
    'halspang',
    'italypaleale',
    'johnewart',
    'joshvanl',
    'kaibocai',
    'mcandeia',
    'msfussell',
    'mukundansundar',
    'pkedy',
    'pruthvidhodda',
    'robertojrojas',
    'ryanlettieri',
    'shivamkm07',
    'shubham1172',
    'skyao',
    'taction',
    'tanvigour',
    'yaron2',
    'rabollin',
    'ashiquemd'
]

const SDKs = [
    'dotnet-sdk',
    'go-sdk',
    'java-sdk',
    'js-sdk',
    'python-sdk',
    'php-sdk',
]

const docsIssueBodyTpl = (
    issueNumber
) => `This issue was automatically created by \
[Dapr Bot](https://github.com/dapr/dapr/blob/master/.github/workflows/dapr-bot.yml) because a \"docs-needed\" label \
was added to dapr/dapr#${issueNumber}. \n\n\
TODO: Add more details as per [this template](.github/ISSUE_TEMPLATE/new-content-needed.md).`

const sdkIssueBodyTpl = (
    issueNumber
) => `This issue was automatically created by \
[Dapr Bot](https://github.com/dapr/dapr/blob/master/.github/workflows/dapr-bot.yml) because a \"sdk-needed\" label \
was added to dapr/dapr#${issueNumber}. \n\n\
TODO: Add more details.`

module.exports = async ({ github, context }) => {
    if (
        context.eventName == 'issue_comment' &&
        context.payload.action == 'created'
    ) {
        await handleIssueCommentCreate({ github, context })
    } else if (
        context.eventName == 'issues' &&
        context.payload.action == 'labeled'
    ) {
        await handleIssueLabeled({ github, context })
    } else {
        console.log(`[main] event ${context.eventName} not supported, exiting.`)
    }
}

/**
 * Handle issue comment create event.
 */
async function handleIssueCommentCreate({ github, context }) {
    const payload = context.payload
    const issue = context.issue
    const username = context.actor.toLowerCase()
    const isFromPulls = !!payload.issue.pull_request
    const commentBody = ((payload.comment.body || '') + '').trim()

    if (!commentBody || !commentBody.startsWith('/')) {
        // Not a command
        return
    }

    const commandParts = commentBody.split(/\s+/)
    const command = commandParts.shift()

    // Commands that can be executed by anyone.
    if (command == '/assign') {
        await cmdAssign(github, issue, username, isFromPulls)
        return
    }

    // Commands that can only be executed by owners.
    if (!owners.includes(username)) {
        console.log(
            `[handleIssueCommentCreate] user ${username} is not an owner, exiting.`
        )
        await commentUserNotAllowed(github, issue, username)
        return
    }

    switch (command) {
        case '/make-me-laugh':
            await cmdMakeMeLaugh(github, issue)
            break
        case '/ok-to-test':
            await cmdOkToTest(github, issue, isFromPulls)
            break
        case '/ok-to-perf':
            await cmdOkToPerf(
                github,
                issue,
                isFromPulls,
                commandParts.join(' ')
            )
            break
        case '/ok-to-perf-components':
            await cmdOkToPerfComponents(
                github,
                issue,
                isFromPulls,
                commandParts.join(' ')
            )
            break
        case '/test-sdk-all':
        case '/test-sdk-java':
        case '/test-sdk-python':
        case '/test-sdk-js':
        case '/test-sdk-go':
            await cmdTestSDK(
                github,
                issue,
                isFromPulls,
                command,
                commandParts.join(' ')
            )
            break
        case '/test-version-skew':
            const previousVersion =
                commandParts.length > 0 ? commandParts.shift() : null
            await cmdTestVersionSkew(
                github,
                issue,
                isFromPulls,
                command,
                previousVersion,
                commandParts.join(' ')
            )
            break
        default:
            console.log(
                `[handleIssueCommentCreate] command ${command} not found, exiting.`
            )
            break
    }
}

/**
 * Handle issue labeled event.
 */
async function handleIssueLabeled({ github, context }) {
    const payload = context.payload
    const label = payload.label.name
    const issueNumber = payload.issue.number

    // This should not run in forks.
    if (context.repo.owner !== 'dapr') {
        console.log('[handleIssueLabeled] not running in dapr repo, exiting.')
        return
    }

    // Authorization is not required here because it's triggered by an issue label event.
    // Only authorized users can add labels to issues.
    if (label == 'docs-needed') {
        // Open a new issue
        await github.rest.issues.create({
            owner: 'dapr',
            repo: 'docs',
            title: `New content needed for dapr/dapr#${issueNumber}`,
            labels: ['content/missing-information', 'created-by/dapr-bot'],
            body: docsIssueBodyTpl(issueNumber),
        })
    } else if (label == 'sdk-needed') {
        // Open an issue in all SDK repos.
        for (const sdk of SDKs) {
            await github.rest.issues.create({
                owner: 'dapr',
                repo: sdk,
                title: `Add support for dapr/dapr#${issueNumber}`,
                labels: ['enhancement', 'created-by/dapr-bot'],
                body: sdkIssueBodyTpl(issueNumber),
            })
        }
    } else {
        console.log(
            `[handleIssueLabeled] label ${label} not supported, exiting.`
        )
    }
}

/**
 * Assign the issue to the user who commented.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {string} username GitHub user who commented
 * @param {boolean} isFromPulls is the workflow triggered by a pull request?
 */
async function cmdAssign(github, issue, username, isFromPulls) {
    if (isFromPulls) {
        console.log(
            '[cmdAssign] pull requests unsupported, skipping command execution.'
        )
        return
    } else if (issue.assignees && issue.assignees.length !== 0) {
        console.log(
            '[cmdAssign] issue already has assignees, skipping command execution.'
        )
        return
    }

    await github.rest.issues.addAssignees({
        owner: issue.owner,
        repo: issue.repo,
        issue_number: issue.number,
        assignees: [username],
    })
}

/**
 * Comment a funny joke.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 */
async function cmdMakeMeLaugh(github, issue) {
    const result = await github.request(
        'https://official-joke-api.appspot.com/random_joke'
    )
    jokedata = result.data
    joke = 'I have a bad feeling about this.'
    if (jokedata && jokedata.setup && jokedata.punchline) {
        joke = `${jokedata.setup} - ${jokedata.punchline}`
    }

    await github.rest.issues.createComment({
        owner: issue.owner,
        repo: issue.repo,
        issue_number: issue.number,
        body: joke,
    })
}

/**
 * Trigger e2e test for the pull request.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {boolean} isFromPulls is the workflow triggered by a pull request?
 */
async function cmdOkToTest(github, issue, isFromPulls) {
    if (!isFromPulls) {
        console.log(
            '[cmdOkToTest] only pull requests supported, skipping command execution.'
        )
        return
    }

    // Get pull request
    const pull = await github.rest.pulls.get({
        owner: issue.owner,
        repo: issue.repo,
        pull_number: issue.number,
    })

    if (pull && pull.data) {
        // Get commit id and repo from pull head
        const testPayload = {
            pull_head_ref: pull.data.head.sha,
            pull_head_repo: pull.data.head.repo.full_name,
            command: 'ok-to-test',
            issue: issue,
        }

        // Fire repository_dispatch event to trigger e2e test
        await github.rest.repos.createDispatchEvent({
            owner: issue.owner,
            repo: issue.repo,
            event_type: 'e2e-test',
            client_payload: testPayload,
        })

        console.log(
            `[cmdOkToTest] triggered E2E test for ${JSON.stringify(
                testPayload
            )}`
        )
    }
}

/**
 * Trigger performance test for the pull request.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {boolean} isFromPulls is the workflow triggered by a pull request?
 */
async function cmdOkToPerf(github, issue, isFromPulls, args) {
    if (!isFromPulls) {
        console.log(
            '[cmdOkToPerf] only pull requests supported, skipping command execution.'
        )
        return
    }

    // Get pull request
    const pull = await github.rest.pulls.get({
        owner: issue.owner,
        repo: issue.repo,
        pull_number: issue.number,
    })

    if (pull && pull.data) {
        // Get commit id and repo from pull head
        const perfPayload = {
            pull_head_ref: pull.data.head.sha,
            pull_head_repo: pull.data.head.repo.full_name,
            command: 'ok-to-perf',
            args,
            issue: issue,
        }

        // Fire repository_dispatch event to trigger e2e test
        await github.rest.repos.createDispatchEvent({
            owner: issue.owner,
            repo: issue.repo,
            event_type: 'perf-test',
            client_payload: perfPayload,
        })

        console.log(
            `[cmdOkToPerf] triggered perf test for ${JSON.stringify(
                perfPayload
            )}`
        )
    }
}

/**
 * Trigger components performance test for the pull request.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {boolean} isFromPulls is the workflow triggered by a pull request?
 */
async function cmdOkToPerfComponents(github, issue, isFromPulls, args) {
    if (!isFromPulls) {
        console.log(
            '[cmdOkToPerfComponents] only pull requests supported, skipping command execution.'
        )
        return
    }

    // Get pull request
    const pull = await github.rest.pulls.get({
        owner: issue.owner,
        repo: issue.repo,
        pull_number: issue.number,
    })

    if (pull && pull.data) {
        // Get commit id and repo from pull head
        const perfPayload = {
            pull_head_ref: pull.data.head.sha,
            pull_head_repo: pull.data.head.repo.full_name,
            command: 'ok-to-perf-components',
            args,
            issue: issue,
        }

        // Fire repository_dispatch event to trigger e2e test
        await github.rest.repos.createDispatchEvent({
            owner: issue.owner,
            repo: issue.repo,
            event_type: 'components-perf-test',
            client_payload: perfPayload,
        })

        console.log(
            `[cmdOkToPerfComponents] triggered perf test for ${JSON.stringify(
                perfPayload
            )}`
        )
    }
}

/**
 * Trigger SDK test(s) for the pull request.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {boolean} isFromPulls is the workflow triggered by a pull request?
 * @param {string} command which was used
 */
async function cmdTestSDK(github, issue, isFromPulls, command, args) {
    if (!isFromPulls) {
        console.log(
            '[cmdTestSDK] only pull requests supported, skipping command execution.'
        )
        return
    }

    // Get pull request
    const pull = await github.rest.pulls.get({
        owner: issue.owner,
        repo: issue.repo,
        pull_number: issue.number,
    })

    if (pull && pull.data) {
        // Get commit id and repo from pull head
        const testSDKPayload = {
            pull_head_ref: pull.data.head.sha,
            pull_head_repo: pull.data.head.repo.full_name,
            command: command.substring(1),
            args,
            issue: issue,
        }

        // Fire repository_dispatch event to trigger e2e test
        await github.rest.repos.createDispatchEvent({
            owner: issue.owner,
            repo: issue.repo,
            event_type: command.substring(1),
            client_payload: testSDKPayload,
        })

        console.log(
            `[cmdTestSDK] triggered SDK test for ${JSON.stringify(
                testSDKPayload
            )}`
        )
    }
}

/**
 * Sends a comment when the user who tried triggering the bot action is not allowed to do so.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {string} username GitHub user who commented
 */
async function commentUserNotAllowed(github, issue, username) {
    await github.rest.issues.createComment({
        owner: issue.owner,
        repo: issue.repo,
        issue_number: issue.number,
        body: `👋 @${username}, my apologies but I can't perform this action for you because your username is not in the allowlist in the file ${'`.github/scripts/dapr_bot.js`'}.`,
    })
}

/**
 * Trigger Version Skew tests for the pull request.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {boolean} isFromPulls is the workflow triggered by a pull request?
 * @param {string} command which was used
 * @param {string} previousVersion previous version to test against
 */
async function cmdTestVersionSkew(
    github,
    issue,
    isFromPulls,
    command,
    previousVersion,
    args
) {
    if (!isFromPulls) {
        console.log(
            '[cmdTestVersionSkew] only pull requests supported, skipping command execution.'
        )
        return
    }

    // Get pull request
    const pull = await github.rest.pulls.get({
        owner: issue.owner,
        repo: issue.repo,
        pull_number: issue.number,
    })

    if (pull && pull.data) {
        // Get commit id and repo from pull head
        const testVersionSkewPayload = {
            pull_head_ref: pull.data.head.sha,
            pull_head_repo: pull.data.head.repo.full_name,
            command: command.substring(1),
            previous_version: previousVersion,
            args,
            issue: issue,
        }

        // Fire repository_dispatch event to trigger e2e test
        await github.rest.repos.createDispatchEvent({
            owner: issue.owner,
            repo: issue.repo,
            event_type: command.substring(1),
            client_payload: testVersionSkewPayload,
        })

        console.log(
            `[cmdTestVersionSkew] triggered Version Skew test for ${JSON.stringify(
                testVersionSkewPayload
            )}`
        )
    }
}
