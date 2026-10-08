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

The Installed tab is a private module (`installedTab`, ADR-0004). Its
`update(msg) (tea.Cmd, installedStatus)` entry owns prune admission, confirmation
dialog, inline delete target, teardown, and disk summary. Model routes input and
results and applies status, navigation, and catalog effects; the shared projection
still owns the table, version mutations, and delete revalidation. Preview/run
results carry a prune request ID distinct from catalog operation IDs: leaving
Installed invalidates an unconfirmed preview, while a confirmed run survives and
accepts completion exactly once, even on another tab or under Help. Test through
keys and emitted command results, not the module's private state.

Mouse input uses Bubble Tea's last-rendered `View.OnMouse` surface. Build cell
rectangles alongside the rendered fragments, clip them with the same viewport,
and dispatch semantic actions through the existing key and operation handlers.
Do not pass raw mouse events into the list, table, or text inputs a second time.
Clicks carry an interactive revision and full row identities; wheel bursts
retain their context without requiring a matching revision. Installed and Deps
use `rowTable`, which owns the visible window as well as the cursor.
Bubble Tea 2.0.10 skips `OnMouse` updates when the visible view fields compare
equal. `mouseFrameContent` therefore carries the full revision in a zero-cell
OSC 8 close marker; removing it freezes clicks after visually unchanged updates.

The bottom panel and modal footers share `renderControls`: single-line
`[ Action key ]` buttons with clickable brackets and inner padding, separated by
one non-clickable column. Short registry sections wrap as a group; longer sections
wrap by whole buttons. Measure terminal-cell widths, not byte lengths.
Let `chrome`/`relayout` measure footer height and `joinSurfaces` translate targets;
do not reserve a fixed footer height. Keep both themes, PATH warnings, Installed
summaries, and long Unicode filters in the viewport matrix. Test button edges,
padding, wrapped rows, and gaps using coordinates from the visible frame.

Settings values are `[ value ]` buttons: clip long values inside the brackets,
reserve room for backup-limit step buttons, and include brackets and padding in
the activation rectangle. Dependency selection controls use all three cells of
`[○]` / `[●]` as the mark target; the following gap and module path only select
the row. Keep cursor highlighting separate from update marks. Table-cell styles
own padding; the selected-row wrapper must not add spacing or shift columns.

Mouse regressions should locate coordinates in the visible rendered text, not
read the hit targets they are testing. Cover selection separately from execution,
modal isolation, delayed clicks, wheel bursts, filtering, and 64×20 layouts.
Run `go test ./internal/model ./internal/setup -run 'Test(Mouse|RowTable|SetupMouse)'`
and `go test -race ./internal/model ./internal/setup`, then exercise SGR mouse
events in a real PTY with an isolated HOME and a temporary module. Direct model
tests alone do not exercise the renderer's last-frame callback lifecycle.

## Report an Issue

If you have run into a bug or want to discuss a new feature, please [file an issue](https://github.com/SmileOniks/govm/issues).

## Contributing Code with Pull Requests

GoVM uses [Github pull requests](https://github.com/SmileOniks/govm/pulls). Feel free to fork, hack away at your changes and submit.

### Requirements

 *  All commands and functionality should be documented appropriately
 *  All new functionality/features should have appropriate unit testing

GoVM strives to have a consistent set of documentation that matches the command structure and any new functionality must have accompanying documentation in the PR. Changes to dependency backups must keep `govm deps backups` and `govm deps restore <file>` synchronized across CLI help, README documentation, and unit tests. `govm doctor` is read-only: a new Check must not take the state lock, write under `~/.govm`, or reach the network outside the `--offline`-skippable source Check, and its verdict wording must be mirrored in the README "Diagnostics" section.

## Licensing

See the [LICENSE](https://github.com/SmileOniks/govm/blob/main/LICENSE) file for our project's licensing. We will ask you to confirm the licensing of your contribution.
