# Releasing khsier and updating Homebrew

The release workflow validates an annotated `v` SemVer tag, builds the release
archives, and publishes those archives with `SHA256SUMS`. After publication
succeeds, ordinary `vX.Y.Z` tags call the reusable workflow in
`zaubermaerchen/homebrew-tap` to propose a Formula update as a pull request.
Prerelease tags (such as `v1.2.3-alpha`) and tags with build metadata (such as
`v1.2.3+build`) still publish releases, but skip the Homebrew update. Homebrew
users receive the update after that pull request is reviewed and merged.

Formula rewriting, release validation, and pull request creation belong to the
tap. khsier only supplies its Formula name, validated tag, automation revision,
and GitHub App credentials. See the tap's
[release automation guide](https://github.com/zaubermaerchen/homebrew-tap/blob/main/docs/release-automation.md)
for the shared contract, App setup, and validation commands.

## Initial setup

1. Deploy and validate the common workflow in `homebrew-tap` first. Do not enable
   the khsier caller before that workflow and its script exist in the tap.
2. Create a GitHub App installed only on `zaubermaerchen/homebrew-tap`, with
   repository **Contents: read and write**, **Pull requests: read and write**,
   and the automatically required **Metadata: read** permission. No organization
   permissions or installation on khsier are needed. Follow the common guide to
   create the App and generate its private key.
3. In khsier's **Settings → Secrets and variables → Actions**, set repository
   variable `HOMEBREW_TAP_APP_ID` to the App ID and repository secret
   `HOMEBREW_TAP_APP_PRIVATE_KEY` to the complete PEM private key. The caller's
   `GITHUB_TOKEN` has only `contents: read`; the App provides tap access.
4. Replace both `update-formula.yml@main` and `automation-ref: main` in
   `.github/workflows/release.yml` with the same reviewed 40-character tap commit
   SHA containing the automation. For Draft PR validation, this may be a published
   commit from the tap PR, so GitHub can resolve the reusable workflow before it
   is merged. The `main` references are local draft values; pin the reviewed
   commit before opening the khsier Draft PR. Merge the tap automation before
   enabling the khsier caller, and use immutable references for deployed releases.
5. Run `go test ./...`, `go vet ./...`, and workflow validation. For the first
   release, confirm that release publication succeeds and that the resulting
   tap PR changes only `Formula/khsier.rb`. Run the shared Formula validation
   before merging that PR.

The Homebrew job is skipped for pull requests, non-ordinary version tags, and
failed release publication.
Missing App configuration causes a failed Homebrew job; it does not silently
skip the update. A Homebrew failure does not remove the already published
release. Once configuration is repaired, rerun the failed Homebrew job rather
than publishing the existing release again.

## Other tools

After khsier's integration is validated, pipewisp, dam, outage, and sluice can
call the same pinned workflow after their release publication jobs, using their
own Formula name and validated release tag. Configure the same App ID and
private key as repository Actions settings in each caller. Keep Formula update
logic in the tap, and follow the common guide's supported asset naming and
checksum contract before enabling each tool.
