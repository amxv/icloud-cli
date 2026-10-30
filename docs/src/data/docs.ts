export const siteConfig = {
  name: "icloud",
  strapline: "iCloud Mail from the command line",
  description:
    "A friendly command-line interface for reading, searching, composing, organizing, and automating iCloud Mail.",
  repoUrl: "https://github.com/amxv/icloud-cli",
  accentColor: "#0369a1",
  accentColorDark: "#38bdf8",
  footerSections: [
    {
      title: "icloud",
      text:
        "A capable iCloud Mail command-line client for people, scripts, and agents."
    },
    {
      title: "Mail",
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
