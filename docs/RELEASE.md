# Release pipeline

amatoken uses [semantic-release](https://semantic-release.gitbook.io/) wired
through the reusable workflow at
[`Bedatty-Engineering/modules-hub@v1.4.2`](https://github.com/Bedatty-Engineering/modules-hub).
Versions and changelogs come from **Conventional Commits**; binaries are built
in a follow-up workflow and attached to the GitHub Release.

## Branch model

| Branch | Channel | Tag format |
|---|---|---|
| `main` | `latest` (stable) | `vX.Y.Z` |
| `dev`  | `alpha` (prerelease) | `vX.Y.Z-alpha.N` |

A merge to `dev` produces an alpha; a merge to `main` produces (or promotes to)
a stable release. The `v<MAJOR>` floating tag (e.g. `v1`) is updated only on
stable releases on `main`.

## Commit conventions

Release type is decided from commit type:

| Type | Effect |
|---|---|
| `feat:` | minor bump |
| `fix:` / `perf:` / `refactor:` | patch bump |
| `<type>!:` or `BREAKING CHANGE:` footer | major bump |
| `docs:` / `style:` / `test:` / `chore:` / `ci:` | no release |

Use `feat(scope): …` to give the changelog a section.

## Files in the repo

| Path | Purpose |
|---|---|
| `release.config.js` | semantic-release config with branch-aware plugin selection. |
| `package.json` + `package-lock.json` | semantic-release toolchain (devDeps only — not shipped). |
| `.github/workflows/validate.yml` | Caller — invokes `modules-hub` PR/push validation for hygiene and workflow checks. |
| `.github/workflows/release.yml` | Local release workflow for `main` / `dev`, reusing `modules-hub` composites. |
| `.github/workflows/build.yml` | Triggers when a GitHub Release is published, checks out the release tag, builds binaries, uploads them to the GitHub Release, and pushes the Docker image to GHCR. |
| `CHANGELOG.md` | Generated and committed by semantic-release. |

## One-time setup (repo admin)

The release workflow needs three secrets configured under
**Settings → Secrets and variables → Actions**:

### 1. `RELEASE_TOKEN`

A Personal Access Token (classic or fine-grained) with write access to this
repo's contents, issues and pull requests. Used by `actions/checkout` and by
semantic-release to push the changelog commit and create releases.

> A fine-grained PAT scoped to `Bedatty-Engineering/amatoken` with
> `Contents: Read and write`, `Issues: Read and write`, `Pull requests: Read and write`
> is enough.

```bash
gh secret set RELEASE_TOKEN --repo Bedatty-Engineering/amatoken --body "$YOUR_PAT"
```

### 2. `GPG_PRIVATE_KEY` + `GPG_PASSPHRASE`

semantic-release commits the changelog, and the release composite signs that
commit so the release tag/commit show as **Verified** in GitHub.

Generate a key dedicated to this bot identity:

```bash
gpg --batch --gen-key <<EOF
%echo Generating amatoken release key
Key-Type: ed25519
Key-Curve: ed25519
Key-Usage: sign
Subkey-Type: ed25519
Subkey-Curve: ed25519
Subkey-Usage: sign
Name-Real: amatoken-release
Name-Email: amatoken-release@users.noreply.github.com
Expire-Date: 2y
Passphrase: <pick-something>
%commit
EOF

# Export armored private key
gpg --armor --export-secret-keys amatoken-release@users.noreply.github.com > /tmp/release.key

# Export armored public key (add this to the GitHub user that owns the PAT
# above, under Settings → SSH and GPG keys → New GPG key)
gpg --armor --export amatoken-release@users.noreply.github.com
```

Push to repo secrets:

```bash
gh secret set GPG_PRIVATE_KEY --repo Bedatty-Engineering/amatoken < /tmp/release.key
gh secret set GPG_PASSPHRASE  --repo Bedatty-Engineering/amatoken --body "<the-passphrase>"
shred -u /tmp/release.key
```

> The public key must be attached to the GitHub user that owns the
> `RELEASE_TOKEN` PAT — otherwise commits won't show as Verified even though
> they're signed.

### 3. (Optional) Branch protection

If `main` has branch protection enabled, allow the bot user to bypass required
status checks for the changelog commit, or exclude `[skip ci]` commits from
the protected paths. Otherwise the post-release commit will be rejected.

## How a release happens

1. PR is merged to `dev` or `main`.
2. `release.yml` triggers and runs a local job that:
   - checks out with full history
   - imports the GPG key
   - runs `npx semantic-release` against the triggering branch
   - on `main`, uses changelog/git/github plugins and moves the floating `v<major>` tag
   - on `dev`, uses a reduced prerelease plugin set that avoids GitHub Release publication
3. semantic-release tags `vX.Y.Z` (or `vX.Y.Z-alpha.N`) and pushes it.
4. Stable GitHub Releases on `main` trigger `build.yml` after the tag already exists, and it:
   - builds for `linux/{amd64,arm64}` and `darwin/{amd64,arm64}`
   - uploads `amatoken-<os>-<arch>` + `amatoken-<os>-<arch>.sha256` to the release
   - builds and pushes a multi-arch image to `ghcr.io/Bedatty-Engineering/amatoken`

## Local sanity check (no publish)

To verify the toolchain installs correctly without releasing:

```bash
npm ci
npx semantic-release --dry-run --no-ci
```

`--dry-run` prints what *would* be released and exits cleanly.
