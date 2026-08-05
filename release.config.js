const prerelease = process.env.SR_PRERELEASE === "true";

const commitAnalyzer = [
  "@semantic-release/commit-analyzer",
  {
    preset: "conventionalcommits",
    releaseRules: [
      { type: "feat", release: "minor" },
      { type: "fix", release: "patch" },
      { type: "perf", release: "patch" },
      { type: "refactor", release: "patch" },
      { type: "docs", release: false },
      { type: "style", release: false },
      { type: "test", release: false },
      { type: "chore", release: false },
      { type: "ci", release: false },
      { breaking: true, release: "major" },
    ],
  },
];

const releaseNotes = [
  "@semantic-release/release-notes-generator",
  { preset: "conventionalcommits" },
];

const stableOnlyPlugins = [
  ["@semantic-release/changelog", { changelogFile: "CHANGELOG.md" }],
  [
    "@semantic-release/git",
    {
      assets: ["CHANGELOG.md"],
      message:
        "chore(release): ${nextRelease.version} [changelog] [skip ci]\\n\\n${nextRelease.notes}",
    },
  ],
  "@semantic-release/github",
];

export default {
  branches: [
    { name: "main", channel: "latest" },
    { name: "dev", prerelease: "alpha" },
  ],
  plugins: prerelease
    ? [commitAnalyzer, releaseNotes]
    : [commitAnalyzer, releaseNotes, ...stableOnlyPlugins],
};
