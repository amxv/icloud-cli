export const siteConfig = {
  name: "icloud-cli",
  strapline: "A Go CLI shipped through npm",
  description:
    "Documentation for icloud-cli, a starter Go command-line tool template with npm distribution, GitHub release automation, and an embedded ZueDocs-powered docs site.",
  repoUrl: "https://github.com/amxv/icloud-cli",
  accentColor: "#0369a1",
  accentColorDark: "#38bdf8",
  footerSections: [
    {
      title: "icloud-cli",
      text:
        "A starter Go CLI template with native binaries, npm installation, release automation, and docs included from day one."
    },
    {
      title: "What this site covers",
      text:
        "Installation, local development, command customization, release workflow, npm packaging, and docs site maintenance."
    },
    {
      title: "Repository",
      linkPrefix: "Source: ",
      linkHref: "https://github.com/amxv/icloud-cli",
      linkLabel: "github.com/amxv/icloud-cli"
    }
  ]
} as const;

export const docCategories = [
  "Start",
  "Development",
  "Distribution",
  "Reference"
] as const;

export const primaryNav = [
  { href: "/docs", label: "Docs" },
  { href: siteConfig.repoUrl, label: "GitHub", external: true }
];
