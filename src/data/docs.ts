export const siteConfig = {
  name: "icloud",
  strapline: "iCloud from the command line",
  description:
    "Install and use icloud, a local-first CLI designed for compact human and agent workflows.",
  repoUrl: "https://github.com/amxv/icloud-cli",
  accentColor: "#0369a1",
  accentColorDark: "#38bdf8",
  footerSections: [
    {
      title: "icloud",
      text:
        "A local-first command-line client for iCloud services with compact output by default."
    },
    {
      title: "Current command set",
      text:
        "Secure account lifecycle, read-only IMAP/SMTP validation, durable folder discovery, bounded enabled-folder sync with history-free Sent setup, multi-location identity reconciliation, location-aware read/flag updates, complete local message reads, raw and attachment export, persisted health diagnostics, shell completions, and man pages."
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
