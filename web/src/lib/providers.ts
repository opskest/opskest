export type ProviderCategory =
  | 'Cloud & hosting'
  | 'CDN & DNS'
  | 'Payments & commerce'
  | 'Identity & security'
  | 'Developer tools'
  | 'Communication'
  | 'Observability'
  | 'AI & data'
  | 'Productivity & SaaS'

export const PROVIDER_CATEGORIES: ProviderCategory[] = [
  'Cloud & hosting', 'CDN & DNS', 'Payments & commerce', 'Identity & security', 'Developer tools',
  'Communication', 'Observability', 'AI & data', 'Productivity & SaaS',
]

export interface ProviderPreset { name: string; url: string; desc: string; brand: string; category: ProviderCategory }

/**
 * Brands that ship a local logo in /public/providers (see NOTICE.md there and
 * sources.json for each file's provenance and licence). A brand not listed here
 * renders a deterministic monogram tile instead of a remote or redrawn mark, so
 * a missing asset never produces a broken image. web/tests/providers.test.mjs
 * requires every preset to resolve to a shipped file or to appear in its
 * explicit monogram allowlist.
 */
export const BRAND_ASSETS: Record<string, 'png' | 'svg'> = {
  firebase: 'png', google: 'png', slack: 'png',
  googlecloud: 'svg', github: 'svg', meta: 'svg', whatsapp: 'svg', cloudflare: 'svg', openai: 'svg', stripe: 'svg',
  digitalocean: 'svg', vercel: 'svg', netlify: 'svg', render: 'svg', twilio: 'svg', discord: 'svg', zoom: 'svg',
  datadog: 'svg', sentry: 'svg', figma: 'svg', bitbucket: 'svg', jira: 'svg', confluence: 'svg', trello: 'svg', gitlab: 'svg',
  // Cloud & hosting
  aws: 'svg', heroku: 'svg', linode: 'svg', scaleway: 'svg', flyio: 'svg', supabase: 'svg', planetscale: 'svg', mongodbatlas: 'svg',
  // CDN & DNS
  bunny: 'svg', cloudinary: 'svg', godaddy: 'svg',
  // Payments & commerce
  paddle: 'svg', coinbase: 'svg', bigcommerce: 'svg', squarespace: 'svg', wix: 'svg', webflow: 'svg',
  // Identity & security
  clerk: 'svg', onepassword: 'svg', snyk: 'svg',
  // Developer tools
  npm: 'svg', pypi: 'svg', docker: 'svg', circleci: 'svg', hashicorp: 'svg', postman: 'svg', linear: 'svg', expo: 'svg', bitrise: 'svg',
  mapbox: 'svg', contentful: 'svg', sanity: 'svg', prisma: 'svg',
  // Communication
  mailgun: 'svg', webex: 'svg', pusher: 'svg', vimeo: 'svg',
  // Observability
  newrelic: 'svg', grafana: 'svg', elastic: 'svg', rollbar: 'svg', mixpanel: 'svg',
  // AI & data
  anthropic: 'svg', perplexity: 'svg', snowflake: 'svg',
  // Productivity & SaaS
  atlassian: 'svg', notion: 'svg', asana: 'svg', airtable: 'svg', zapier: 'svg', dropbox: 'svg', box: 'svg', miro: 'svg',
  canva: 'svg', calendly: 'svg', typeform: 'svg', hubspot: 'svg',
}

/**
 * Marks whose colour is too light to read on the white plate (contrast under
 * 2.2:1 against white) sit on a dark plate instead, like Firebase's official
 * asset. providers.test.mjs recomputes the contrast of every mark against the
 * plate it gets, so adding a pale logo without listing it here fails the test.
 */
export const DARK_PLATE_BRANDS: ReadonlySet<string> = new Set([
  'firebase', 'paddle', 'newrelic', 'godaddy', 'render', 'bunny', 'supabase', 'whatsapp', 'airtable', 'netlify', 'canva',
])

const slug = (name: string) => name.toLowerCase().replace(/[^a-z0-9]+/g, '')
const p = (name: string, url: string, desc: string, category: ProviderCategory, brand = slug(name)): ProviderPreset => ({ name, url, desc, brand, category })

/**
 * Official status pages and incident feeds. The first block is the original
 * catalog (feed checks recorded in public/providers/feed-sources.json). Every
 * entry was also run through the real vendor checker by the live provider
 * check (internal/check/providers_live_test.go, see Docs.md, "Third-party provider feeds") and
 * kept only if the checker could read incidents from it; that is a
 * point-in-time result, and vendors move their pages, so rerun it before
 * trusting the list. Statuspage style hosts (status.<vendor>.com) are read
 * through the Statuspage summary API; anything with rss/feed in its path is
 * read as an RSS or Atom feed. The Connect form lets an admin edit the
 * address before saving.
 */
export const PROVIDER_PRESETS: ProviderPreset[] = [
  p('Google Firebase', 'https://status.firebase.google.com/', 'Firestore, Authentication, Functions, Hosting', 'Cloud & hosting', 'firebase'),
  p('Google Cloud', 'https://status.cloud.google.com/', 'Compute, storage, networking, BigQuery', 'Cloud & hosting', 'googlecloud'),
  p('Google Workspace', 'https://www.google.com/appsstatus/dashboard/', 'Gmail, Drive, Meet, Calendar', 'Productivity & SaaS', 'google'),
  p('GitHub Status', 'https://www.githubstatus.com/', 'Git, Actions, Packages, API', 'Developer tools', 'github'),
  p('Meta Graph API', 'https://metastatus.com/graph-api', 'Facebook and Instagram Graph API', 'Communication', 'meta'),
  p('Meta WhatsApp Business API', 'https://metastatus.com/whatsapp-business-api', 'Cloud API, messaging, business accounts', 'Communication', 'whatsapp'),
  p('Meta Marketing API', 'https://metastatus.com/marketing-api', 'Campaigns, ads, reporting', 'Communication', 'meta'),
  p('Meta Ads Manager', 'https://metastatus.com/ads-manager', 'Ad creation, billing, delivery', 'Communication', 'meta'),
  p('Cloudflare Status', 'https://www.cloudflarestatus.com/', 'Edge network, DNS, Workers, Pages', 'CDN & DNS', 'cloudflare'),
  p('OpenAI Status', 'https://status.openai.com/', 'API, ChatGPT, model services', 'AI & data', 'openai'),
  p('Slack Status', 'https://status.slack.com/', 'Messaging, workspaces, integrations', 'Communication', 'slack'),
  p('DigitalOcean', 'https://status.digitalocean.com/history.rss', 'Droplets, Kubernetes, databases, storage', 'Cloud & hosting'),
  p('Vercel', 'https://www.vercel-status.com/history.rss', 'Deployments, edge network, functions', 'Cloud & hosting'),
  p('Netlify', 'https://www.netlifystatus.com/history.rss', 'Builds, edge network, functions', 'Cloud & hosting'),
  p('Render', 'https://status.render.com/history.rss', 'Web services, builds, databases', 'Cloud & hosting'),
  p('Twilio', 'https://status.twilio.com/history.rss', 'Messaging, voice, email delivery', 'Communication'),
  p('Discord', 'https://discordstatus.com/history.rss', 'Messaging, voice, gateway, API', 'Communication'),
  p('Zoom', 'https://www.zoomstatus.com/history.rss', 'Meetings, phone, webinars', 'Communication'),
  p('Datadog US1', 'https://status.datadoghq.com/history.rss', 'Metrics, logs, traces, dashboards', 'Observability', 'datadog'),
  p('Sentry', 'https://status.sentry.io/history.rss', 'Error tracking, ingest, alerts', 'Observability'),
  p('Figma', 'https://status.figma.com/history.rss', 'Design files, collaboration, FigJam', 'Productivity & SaaS'),
  p('Bitbucket', 'https://bitbucket.status.atlassian.com/history.rss', 'Repositories, Pipelines, Git, API', 'Developer tools'),
  p('Jira', 'https://jira-software.status.atlassian.com/history.rss', 'Issues, boards, search, automation', 'Developer tools'),
  p('Confluence', 'https://confluence.status.atlassian.com/history.rss', 'Pages, collaboration, search', 'Productivity & SaaS'),
  p('Trello', 'https://trello.status.atlassian.com/history.rss', 'Boards, cards, automation', 'Productivity & SaaS'),
  p('GitLab', 'https://status.gitlab.com/pages/5b36dc6502d06804c08349f7/rss', 'GitLab.com, Git operations, CI/CD', 'Developer tools'),

  // More providers: each kept after the live provider check (Docs.md, "Third-party provider feeds").
  // Cloud & hosting
  p('Amazon Web Services', 'https://status.aws.amazon.com/rss/all.rss', 'EC2, S3, Lambda, RDS and every region', 'Cloud & hosting', 'aws'),
  p('Heroku', 'https://status.heroku.com/feed', 'Dynos, Postgres, Redis, builds', 'Cloud & hosting'),
  p('Linode (Akamai Cloud)', 'https://status.linode.com/history.rss', 'Compute, block storage, Kubernetes', 'Cloud & hosting', 'linode'),
  p('Scaleway', 'https://status.scaleway.com/', 'Instances, Kubernetes, object storage', 'Cloud & hosting'),
  p('Fly.io', 'https://status.flyio.net/', 'Machines, volumes, Postgres, proxy', 'Cloud & hosting', 'flyio'),
  p('Supabase', 'https://status.supabase.com/', 'Postgres, auth, storage, edge functions', 'Cloud & hosting'),
  p('PlanetScale', 'https://www.planetscalestatus.com/history.rss', 'Managed MySQL and Postgres databases', 'Cloud & hosting'),
  p('MongoDB Atlas', 'https://status.mongodb.com/', 'Atlas clusters, backups, search', 'Cloud & hosting', 'mongodbatlas'),
  // CDN & DNS
  p('Bunny.net', 'https://status.bunny.net/', 'CDN, storage, DNS, optimizer', 'CDN & DNS', 'bunny'),
  p('Cloudinary', 'https://status.cloudinary.com/', 'Image and video delivery, transformations', 'CDN & DNS'),
  p('imgix', 'https://status.imgix.com/', 'Image processing and delivery', 'CDN & DNS'),
  p('GoDaddy', 'https://status.godaddy.com/', 'Domains, DNS, hosting, email', 'CDN & DNS'),
  p('DNSimple', 'https://dnsimple.statuspage.io/history.rss', 'Authoritative DNS, domains, certificates', 'CDN & DNS'),
  // Payments & commerce
  p('Paddle', 'https://paddlestatus.com/history.rss', 'Billing, checkout, subscriptions', 'Payments & commerce'),
  p('Plaid', 'https://status.plaid.com/', 'Bank connections and financial data', 'Payments & commerce'),
  p('Coinbase', 'https://status.coinbase.com/', 'Trading, wallets, API', 'Payments & commerce'),
  p('BigCommerce', 'https://status.bigcommerce.com/', 'Storefront, checkout, APIs', 'Payments & commerce'),
  p('Squarespace', 'https://status.squarespace.com/', 'Sites, commerce, domains', 'Payments & commerce'),
  p('Wix', 'https://status.wix.com/', 'Sites, stores, bookings', 'Payments & commerce'),
  p('Webflow', 'https://status.webflow.com/', 'Hosting, CMS, e-commerce', 'Payments & commerce'),
  // Identity & security
  p('Clerk', 'https://status.clerk.com/', 'User authentication and management', 'Identity & security'),
  p('WorkOS', 'https://status.workos.com/', 'SSO, directory sync, audit logs', 'Identity & security'),
  p('Duo', 'https://status.duo.com/', 'Multi-factor authentication', 'Identity & security'),
  p('1Password', 'https://status.1password.com/', 'Sync, sign-in, Connect, SSO', 'Identity & security', 'onepassword'),
  p('Snyk', 'https://status.snyk.io/', 'Vulnerability scanning and fixes', 'Identity & security'),
  // Developer tools
  p('npm', 'https://status.npmjs.org/', 'Registry, publishing, website', 'Developer tools'),
  p('Python Package Index', 'https://status.python.org/', 'PyPI, python.org, documentation', 'Developer tools', 'pypi'),
  p('Docker', 'https://www.dockerstatus.com/history.rss', 'Docker Hub, Build Cloud, Desktop', 'Developer tools'),
  p('CircleCI', 'https://status.circleci.com/', 'Pipelines, runners, API', 'Developer tools'),
  p('HashiCorp', 'https://status.hashicorp.com/', 'HCP, Terraform Cloud, Vault', 'Developer tools'),
  p('Postman', 'https://status.postman.com/', 'API platform, workspaces, monitors', 'Developer tools'),
  p('Linear', 'https://linearstatus.com/history.rss', 'Issues, projects, API, integrations', 'Developer tools'),
  p('Expo', 'https://status.expo.dev/', 'EAS Build, Update, Submit', 'Developer tools'),
  p('Bitrise', 'https://status.bitrise.io/', 'Mobile CI/CD builds', 'Developer tools'),
  p('LaunchDarkly', 'https://status.launchdarkly.com/', 'Feature flags and streaming', 'Developer tools'),
  p('Mapbox', 'https://status.mapbox.com/', 'Maps, geocoding, navigation', 'Developer tools'),
  p('Contentful', 'https://www.contentfulstatus.com/history.rss', 'Headless CMS and delivery APIs', 'Developer tools'),
  p('Sanity', 'https://www.sanity-status.com/history.rss', 'Content lake, API, Studio', 'Developer tools'),
  p('Prisma', 'https://www.prisma-status.com/history.rss', 'Accelerate, Postgres, Optimize', 'Developer tools'),
  // Communication
  p('Mailgun', 'https://status.mailgun.com/', 'Transactional email delivery', 'Communication'),
  p('Klaviyo', 'https://status.klaviyo.com/', 'Email, SMS, flows, profiles', 'Communication'),
  p('Customer.io', 'https://status.customerio.com/', 'Messaging workflows and delivery', 'Communication', 'customerio'),
  p('Webex', 'https://status.webex.com/history.rss', 'Meetings, calling, messaging', 'Communication'),
  p('Pusher', 'https://status.pusher.com/', 'Channels, Beams, realtime APIs', 'Communication'),
  p('Ably', 'https://status.ably.com/', 'Realtime messaging and presence', 'Communication'),
  p('Mux', 'https://status.mux.com/', 'Video encoding, delivery, data', 'Communication'),
  p('Vimeo', 'https://www.vimeostatus.com/history.rss', 'Video hosting, playback, API', 'Communication'),
  // Observability
  p('New Relic', 'https://status.newrelic.com/', 'Telemetry ingest, APM, alerts', 'Observability', 'newrelic'),
  p('Grafana Cloud', 'https://status.grafana.com/', 'Metrics, logs, traces, alerting', 'Observability', 'grafana'),
  p('Elastic Cloud', 'https://status.elastic.co/', 'Elasticsearch, Kibana, observability', 'Observability', 'elastic'),
  p('Honeycomb', 'https://status.honeycomb.io/', 'Event ingest, queries, triggers', 'Observability'),
  p('Rollbar', 'https://status.rollbar.com/', 'Error monitoring and ingest', 'Observability'),
  p('LogRocket', 'https://status.logrocket.com/', 'Session replay and analytics', 'Observability'),
  p('Segment', 'https://status.segment.com/', 'Event collection and destinations', 'Observability'),
  p('Mixpanel', 'https://status.mixpanel.com/', 'Product analytics and ingestion', 'Observability'),
  p('Amplitude', 'https://status.amplitude.com/', 'Analytics, experiments, delivery', 'Observability'),
  // AI & data
  p('Anthropic', 'https://status.claude.com/', 'Claude API, apps and console', 'AI & data', 'anthropic'),
  p('Cohere', 'https://status.cohere.com/', 'Generate, embed and rerank APIs', 'AI & data'),
  p('Perplexity', 'https://status.perplexity.com/', 'Answer engine and API', 'AI & data'),
  p('Pinecone', 'https://status.pinecone.io/', 'Vector database and inference', 'AI & data'),
  p('Snowflake', 'https://status.snowflake.com/', 'Data cloud accounts and services', 'AI & data'),
  // Productivity & SaaS
  p('Atlassian', 'https://status.atlassian.com/', 'Every Atlassian cloud product', 'Productivity & SaaS'),
  p('Notion', 'https://www.notion-status.com/history.rss', 'Pages, databases, API', 'Productivity & SaaS'),
  p('Asana', 'https://status.asana.com/', 'Tasks, projects, integrations', 'Productivity & SaaS'),
  p('Airtable', 'https://status.airtable.com/', 'Bases, automations, API', 'Productivity & SaaS'),
  p('Zapier', 'https://status.zapier.com/', 'Zaps, tables, interfaces', 'Productivity & SaaS'),
  p('Dropbox', 'https://status.dropbox.com/', 'Sync, sharing, Paper, API', 'Productivity & SaaS'),
  p('Box', 'https://status.box.com/', 'Content cloud and API', 'Productivity & SaaS'),
  p('Miro', 'https://status.miro.com/', 'Boards and collaboration', 'Productivity & SaaS'),
  p('Canva', 'https://www.canvastatus.com/history.rss', 'Design editor, sharing, publishing', 'Productivity & SaaS'),
  p('Calendly', 'https://calendlystatus.com/history.rss', 'Scheduling and integrations', 'Productivity & SaaS'),
  p('Typeform', 'https://status.typeform.com/', 'Forms, responses, API', 'Productivity & SaaS'),
  p('HubSpot', 'https://status.hubspot.com/', 'CRM, marketing, service hubs', 'Productivity & SaaS'),
]

/** Short label for the kind of source behind a preset or connected feed. */
export function providerSourceLabel(url: string): string {
  return /rss|feed|\.xml|\.atom/i.test(url) ? 'Incident feed' : 'Status page'
}

/** Exact host matches prevent unrelated user-entered names from borrowing a brand. */
export function providerBrand(url: string): string | null {
  try {
    const parsed = new URL(url)
    const host = parsed.hostname.toLowerCase().replace(/^www\./, '')
    if (host === 'metastatus.com') return parsed.pathname.split('/').includes('whatsapp-business-api') ? 'whatsapp' : 'meta'
    if (host === 'google.com') return parsed.pathname.startsWith('/appsstatus') ? 'google' : null
    const aliases: Record<string, string> = { 'status.github.com': 'github', 'status.cloudflare.com': 'cloudflare', 'slack-status.com': 'slack', 'status.zoom.us': 'zoom', 'status.bitbucket.org': 'bitbucket', 'trellostatus.com': 'trello', 'status.stripe.com': 'stripe', 'status.docker.com': 'docker', 'status.npmjs.com': 'npm', 'health.aws.amazon.com': 'aws', 'status.anthropic.com': 'anthropic', 'status.linear.app': 'linear', 'status.pypi.org': 'pypi' }
    if (aliases[host]) return aliases[host]
    return PROVIDER_PRESETS.find(item => new URL(item.url).hostname.replace(/^www\./, '') === host)?.brand ?? null
  } catch { return null }
}
