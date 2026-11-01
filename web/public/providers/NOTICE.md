# Provider identity assets

These marks identify the external services being monitored. They remain the trademarks of their respective owners and do not imply endorsement of Opskest. This notice applies to the third-party assets only; it does not choose a license for Opskest.

Assets are served locally. The application does not request visitor IP addresses or browsing activity from favicon or logo services.

## Official full-color assets

| File | Source | Brand guidance |
| --- | --- | --- |
| `firebase.png` | https://firebase.google.com/static/images/brand-guidelines/logo-logomark.png | https://firebase.google.com/brand-guidelines |
| `google.png` | https://developers.google.com/identity/images/g-logo.png | https://developers.google.com/identity/branding-guidelines |
| `slack.png` | https://a.slack-edge.com/80588/marketing/img/icons/icon_slack_hash_colored.png | https://slack.com/media-kit |

Google Workspace is represented by the Google G; Firebase and Google Cloud use their own distinct product marks. WhatsApp's status endpoint uses the WhatsApp mark, while other Meta feeds use Meta's mark.

## Vector geometry

The GitHub, Cloudflare, Google Cloud, Meta, WhatsApp, OpenAI, and Stripe vector geometry is from Simple Icons 11.15.0, distributed under CC0-1.0. The collection license is included as `LICENSE.simple-icons.txt`. Their original source URLs are recorded in `sources.json`.

- Collection: https://github.com/simple-icons/simple-icons/tree/11.15.0
- GitHub: https://github.com/logos
- Cloudflare: https://www.cloudflare.com/logo/
- Google Cloud: https://cloud.google.com
- Meta: https://www.meta.com
- WhatsApp: https://about.meta.com/brand/resources/whatsapp/whatsapp-brand
- OpenAI: https://openai.com/brand/
- Stripe: https://stripe.com/about/resources

OpenAI is rendered in black, in accordance with its current monochrome brand guidance. Stripe is rendered in its current purple (#635BFF). Other vector marks retain the brand color recorded by the collection. Geometry is not redrawn. A neutral white plate preserves mark contrast across application themes.

### Catalogue expansion (2026-10-28)

Fifty-seven more provider marks cover most of the remaining catalogue. Each is the unmodified Simple Icons path (CC0-1.0) fetched from the npm package at an exact version through the jsDelivr CDN, with the collection's brand colour added as the `fill` of the root element. Fifty-four come from Simple Icons 11.15.0 (the version above) and three (Fly.io, bunny.net, Rollbar) are not in that release and come from Simple Icons 16.36.0 (https://github.com/simple-icons/simple-icons/tree/16.36.0, CC0-1.0, same licence file). For every file, `sources.json` records the Simple Icons slug and version, the exact fetch URL, the brand's own source page, the licence and the SHA-256 of the local file. Two presets reuse a parent brand's mark: Linode (Akamai Cloud) uses the Akamai mark and MongoDB Atlas uses the MongoDB mark.

Downloaded SVGs were treated as untrusted data: each was checked to contain only `svg`, `title` and `path` elements, no script, event handler, `foreignObject`, style, `url()`, `href` or external reference, and to be under 20 KB. `web/tests/providers.test.mjs` repeats those checks and verifies the recorded hashes.

Marks too pale to read on white (contrast under 2.2:1) are drawn on a dark plate (`DARK_PLATE_BRANDS` in `web/src/lib/providers.ts`). Providers that are not in Simple Icons and whose own brand or press page does not clearly license the logo for this use (Amplitude, for example, asks to be contacted before logo use) have no file; they render a generated monogram tile and are listed in `Docs.md` (Third-party provider feeds).

Original marks were retrieved 2026-10-24; DigitalOcean, Vercel, Netlify, Render, Twilio, Discord, Zoom, Datadog, Sentry, Figma, Bitbucket, Jira, Confluence, Trello, and GitLab were added 2026-10-25 from the same licensed collection. `sources.json` records each mark's original brand source and exact local file hash.

## Curated incident feeds

The expanded catalog uses the official RSS feeds linked by each provider's status page. All fifteen added endpoints returned RSS successfully on 2026-10-25; endpoint URLs and verification timestamps are recorded in `feed-sources.json`. They use the application's existing RSS/Atom parser rather than treating a successful homepage request as service health. Status-page software and URLs can change, so preserve the recorded machine-readable feed when updating a preset.

Official sources include https://status.digitalocean.com/, https://www.vercel-status.com/, https://www.netlifystatus.com/, https://status.render.com/, https://status.twilio.com/, https://discordstatus.com/, https://www.zoomstatus.com/, https://status.datadoghq.com/, https://status.sentry.io/, https://status.figma.com/, https://status.atlassian.com/, and https://status.gitlab.com/.
