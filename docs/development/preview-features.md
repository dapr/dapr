# Preview Features

Feature Toggles (a.k.a [feature flags](https://martinfowler.com/articles/feature-toggles.html)) are a powerful way to change runtime behavior without changing the code itself. We encourage the use of the feature flags especially for preview features - explicit opt-in, and this document is focused on showing best practices/examples, how to configure and how they can be enabled when appropriate (i.e running e2e tests).

Dapr flags for preview features are something between Release Toggles and Ops Toggles and mostly behaving as Kill-Switches with months of longevity.

## Declaring

The set of all possible feature flags are defined by dapr contributors by changing the codebase and the toggle is made by the user (i.e app developer) when configuring the application.

All available feature flags are defined in the `../../pkg/config/configuration.go` file and they are arbitrary strings. We encourage to choose a meaningful name, do not avoid to use longer names if it is necessary, keep in mind that this is the name that will be used by the user when toggling.

> avoid using words like `Enabled`, `Disabled` and `Active` they are redundant with the flag boolean value

## Toggling

Feature flags are enabled/disabled via dapr global configuration.

```yaml
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pluggablecomponentsconfig # arbitrary config name
spec:
  features:
    - name: PluggableComponents # The name you chose
      enabled: true # or false
```

That way the runtime will load the global configuration and make it available to be used for the application.

Note that `features` is a list, so you can activate/deactivate many features at once.

## Using

To check if a feature is available in runtime any time by calling `../../pkg/config/configuration.go#IsFeatureEnabled`

Feature checks are generally made as earlier as possible on code to avoid unnecessary computations when feature is disabled and to make code cleaner for the reader.

Bad :x:

```golang
func doSomething() error {
    if !config.IsFeatureEnabled(doSomethingFeature) {
        // do nothing
        return nil
    }
    // .. doSomething instead
}
```

Good :heavy_check_mark:

```golang
func initSomething() {
    if config.IsFeatureEnabled(doSomethingFeature) {
        doSomething()
    }
}
```

Great, how about e2e tests?

For that we recommend that you create a specific configuration for your app activating the feature,

i.e `../../tests/config/the_configuration_name_goes_here.yaml`

```yaml
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: myappconfig # arbitrary config name
spec:
  features:
    - name: MyFeatureFlag # The name you chose
      enabled: true # or false
```

1. Include your configuration set on `../../tests/dapr_tests.mk` under `setup-test-components` target
2. Start your app pointing to it

```golang
testApps := []kube.AppDescription{
    {
        AppName:        yourApp,
        ImageName:      "e2e-your-app-image",
        Config:         "myappconfig",
    },
}
```

## Documentation

When a new [preview feature](https://docs.dapr.io/operations/support/support-preview-features/) is added our [documentation should be updated](https://github.com/dapr/docs/blob/4674817212c141acd4256a4d3ac441d5559f1eef/daprdocs/content/en/operations/support/support-preview-features.md). As a followup action create a new issue on docs repository, [check an example](https://github.com/dapr/docs/issues/2786).

## Release GA

When the feature flag is no longer needed as the feature has published for General Availability, then all previous steps should be revisited, documentation, code reference and additional settings. Creating a feature flag removal issue for a future milestone is seen as good practice.
