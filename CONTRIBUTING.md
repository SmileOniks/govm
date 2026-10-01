# Contributing

Thanks for helping make GoVM better!
- [Contributing](#contributing)
  - [Design Principles](#design-principles)
  - [Report an Issue](#report-an-issue)
  - [Contributing Code with Pull Requests](#contributing-code-with-pull-requests)
    - [Requirements](#requirements)
  - [Licensing](#licensing)

## Design Principles

Contributions to GoVM should align with the project’s design principles:

 * Maintain backwards compatibility whenever possible.

The Available and Installed tabs share one catalog flow in `internal/model`.
`catalogProjectionAdapter.apply` accepts actions and asynchronous results and
owns admission, operation IDs, dispatch, progress continuation, reconciliation,
and publication. Model selects an identity, routes messages, and applies returned
effects; it must not register or schedule a second copy of a catalog operation.
Manual refresh, initial load, source checks, and reconciliation have distinct
admission rules: do not replace them with a single busy guard. Exercise changes
through keys/messages and the existing `VersionOperations` adapters, including
late results and tab navigation during installation.

## Report an Issue

If you have run into a bug or want to discuss a new feature, please [file an issue](https://github.com/smileoniks-ctrl/govm/issues).

## Contributing Code with Pull Requests

GoVM uses [Github pull requests](https://github.com/smileoniks-ctrl/govm/pulls). Feel free to fork, hack away at your changes and submit.

### Requirements

 *  All commands and functionality should be documented appropriately
 *  All new functionality/features should have appropriate unit testing

GoVM strives to have a consistent set of documentation that matches the command structure and any new functionality must have accompanying documentation in the PR. Changes to dependency backups must keep `govm deps backups` and `govm deps restore <file>` synchronized across CLI help, README documentation, and unit tests. `govm doctor` is read-only: a new Check must not take the state lock, write under `~/.govm`, or reach the network outside the `--offline`-skippable source Check, and its verdict wording must be mirrored in the README "Diagnostics" section.

## Licensing

See the [LICENSE](https://github.com/smileoniks-ctrl/govm/blob/main/LICENSE) file for our project's licensing. We will ask you to confirm the licensing of your contribution.
