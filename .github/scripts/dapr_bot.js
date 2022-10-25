// list of owner who can control dapr-bot workflow
// TODO: Read owners from OWNERS file.
const owners = [
    'addjuarez',
    'artursouza',
    'berndverst',
    'daixiang0',
    'halspang',
    'ItalyPaleAle',
    'johnewart',
    'mcandeia',
    'msfussell',
    'mukundansundar',
    'pkedy',
    'pruthvidhodda',
    'shubham1172',
    'skyao',
    'tanvigour',
    'yaron2'
];

module.exports = async ({ github, context }) => {
    const payload = context.payload;
    const issue = context.issue;
    const username = context.actor;
    const isFromPulls = !!payload.issue.pull_request;
    const commentBody = payload.comment.body;

    if (!commentBody) {
        console.log("[main] comment body not found, exiting.");
        return;
    }
    const command = commentBody.split(" ")[0];

    // Commands that can be executed by anyone.
    if (command === "/assign") {
        await cmdAssign(github, issue, username, isFromPulls);
        return;
    }

    // Commands that can only be executed by owners.
    if (owners.indexOf(username) < 0) {
        console.log(`[main] user ${username} is not an owner, exiting.`);
        return;
    }

    switch (command) {
        case "/make-me-laugh":
            await cmdMakeMeLaugh(github, issue);
            break;
        case "/ok-to-test":
            await cmdOkToTest(github, issue, isFromPulls);
            break;
        case "/ok-to-perf":
            await cmdOkToPerf(github, issue, isFromPulls);
            break;
        default:
            console.log(`[main] command ${command} not found, exiting.`);
            break;
    }
}

/**
 * Assign the issue to the user who commented.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {*} username GitHub user who commented
 * @param {boolean} isFromPulls is the workflow triggered by a pull request?
 */
async function cmdAssign(github, issue, username, isFromPulls) {
    if (isFromPulls) {
        console.log("[cmdAssign] pull requests unsupported, skipping command execution.");
        return;
    } else if (issue.assignees && issue.assignees.length !== 0) {
        console.log("[cmdAssign] issue already has assignees, skipping command execution.");
        return;
    }

    await github.issues.addAssignees({
        owner: issue.owner,
        repo: issue.repo,
        issue_number: issue.number,
        assignees: [username],
    });
}

/**
 * Comment a funny joke.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 */
async function cmdMakeMeLaugh(github, issue) {
    const result = await github.request("https://official-joke-api.appspot.com/random_joke");
    jokedata = result.data;
    joke = "I have a bad feeling about this.";
    if (jokedata && jokedata.setup && jokedata.punchline) {
        joke = `${jokedata.setup} - ${jokedata.punchline}`;
    }

    await github.issues.createComment({
        owner: issue.owner,
        repo: issue.repo,
        issue_number: issue.number,
        body: joke,
    });
}

/**
 * Trigger e2e test for the pull request.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {boolean} isFromPulls is the workflow triggered by a pull request?
 */
async function cmdOkToTest(github, issue, isFromPulls) {
    if (!isFromPulls) {
        console.log("[cmdOkToTest] only pull requests supported, skipping command execution.");
        return;
    }

    // Get pull request
    const pull = await github.pulls.get({
        owner: issue.owner,
        repo: issue.repo,
        pull_number: issue.number
    });

    if (pull && pull.data) {
        // Get commit id and repo from pull head
        const testPayload = {
            pull_head_ref: pull.data.head.sha,
            pull_head_repo: pull.data.head.repo.full_name,
            command: "ok-to-test",
            issue: issue,
        };

        // Fire repository_dispatch event to trigger e2e test
        await github.repos.createDispatchEvent({
            owner: issue.owner,
            repo: issue.repo,
            event_type: "e2e-test",
            client_payload: testPayload,
        });

        console.log(`[cmdOkToTest] triggered E2E test for ${JSON.stringify(testPayload)}`);
    }
}

/**
 * Trigger performance test for the pull request.
 * @param {*} github GitHub object reference
 * @param {*} issue GitHub issue object
 * @param {boolean} isFromPulls is the workflow triggered by a pull request?
 */
async function cmdOkToPerf(github, issue, isFromPulls) {
    if (!isFromPulls) {
        console.log("[cmdOkToPerf] only pull requests supported, skipping command execution.");
        return;
    }

    // Get pull request
    const pull = await github.pulls.get({
        owner: issue.owner,
        repo: issue.repo,
        pull_number: issue.number
    });

    if (pull && pull.data) {
        // Get commit id and repo from pull head
        const perfPayload = {
            pull_head_ref: pull.data.head.sha,
            pull_head_repo: pull.data.head.repo.full_name,
            command: "ok-to-perf",
            issue: issue,
        };

        // Fire repository_dispatch event to trigger e2e test
        await github.repos.createDispatchEvent({
            owner: issue.owner,
            repo: issue.repo,
            event_type: "perf-test",
            client_payload: perfPayload,
        });

        console.log(`[cmdOkToPerf] triggered perf test for ${JSON.stringify(perfPayload)}`);
    }
}
