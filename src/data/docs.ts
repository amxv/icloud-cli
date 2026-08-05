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
        "Local account-state inspection, version and schema capabilities, shell completions, man pages, and stable automation errors."
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
