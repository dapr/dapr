# Version skew tests

Version skew tests validate that systems running mixed versions (e.g., sidecars on an older release and control plane on the latest version, or vice versa) continue to function as expected (important for environments with a rolling upgrade strategy). 
Integration and end-to-end tests on KinD are run in all permutations of master and the latest version of Dapr.

## Preparing version skew test patches for a new version

- Your starting point are the latest [version skew test runs](https://github.com/dapr/dapr/actions/workflows/version-skew.yaml) in Gihub Actions. you can see what needs to be updated in there.
- Checkout the old version:
    - Ex: `git checkout v1.14.4`
- Do the change that would make the test pass
- Create a patch file
    - `git diff > ~/Desktop/0001-update-pubsub-expected-error.diff`
- Reset your changes:
  - `git reset --hard HEAD` 
- Try applying it, just to make sure it works:
    - `git apply --ignore-space-change --ignore-whitespace ~/Desktop/0001-update-pubsub-expected-error.diff`
- Checkout your working branch
- Create the necessary directories under `.github/scripts/version-skew-test-patches/integration` and `.github/scripts/version-skew-test-patches/e2e`, following the existing structure 
- Copy your patch file into the appropriate dir
- Commit, push, open a PR
- Run `/test-version-skew` in your PR to trigger the version skew tests
- Repeat until all tests pass
