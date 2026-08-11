export const siteConfig = {
  name: "icloud",
  strapline: "iCloud from the command line",
  description:
    "A friendly command-line interface for iCloud, starting with a comprehensive iCloud Mail experience for people, scripts, and agents.",
  repoUrl: "https://github.com/amxv/icloud-cli",
  accentColor: "#0369a1",
  accentColorDark: "#38bdf8",
  footerSections: [
    {
      title: "icloud",
      text:
        "A command-line interface for iCloud. Mail is available today; more of the iCloud ecosystem can fit under the same CLI over time."
    },
    {
      title: "Mail today",
      text:
        "Read, search, sync, send, reply, draft, organize, export, automate, and recover iCloud Mail from one command."
    },
    {
      title: "Repository",
      linkPrefix: "Source: ",
      linkHref: "https://github.com/amxv/icloud-cli",
      linkLabel: "github.com/amxv/icloud-cli"
    }
  ]
} as const;

export const docCategories = ["Start", "Mail", "Agents & automation", "Reference"] as const;

export const primaryNav = [
  { href: "/docs", label: "Docs" },
  { href: siteConfig.repoUrl, label: "GitHub", external: true }
];
