export const siteConfig = {
  name: "icloud",
  strapline: "iCloud from the command line",
  description:
    "Install and use icloud, a local-first iCloud Mail CLI designed for compact human and agent workflows.",
  repoUrl: "https://github.com/amxv/icloud-cli",
  accentColor: "#0369a1",
  accentColorDark: "#38bdf8",
  footerSections: [
    {
      title: "icloud",
      text:
        "A local-first iCloud Mail command-line client with compact output by default."
    },
    {
      title: "Current command set",
      text:
        "Account and credential lifecycle, IMAP/SMTP validation, folders, bounded sync/backfill/watch/repair, local and remote search, message/thread/attachment reads, drafts, idempotent send/reply/forward, exact-location state changes, JSON/JSONL automation, schemas, completions, and man pages."
    },
    {
      title: "Repository",
      linkPrefix: "Source: ",
      linkHref: "https://github.com/amxv/icloud-cli",
      linkLabel: "github.com/amxv/icloud-cli"
    }
  ]
} as const;

export const docCategories = ["Start", "Guides", "Reference"] as const;

export const primaryNav = [
  { href: "/docs", label: "Docs" },
  { href: siteConfig.repoUrl, label: "GitHub", external: true }
];
