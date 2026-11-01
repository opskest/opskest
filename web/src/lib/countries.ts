// Countries offered for worldwide checks (Globalping has probes in each).
export const COUNTRY_GROUPS: { region: string; list: [string, string][] }[] = [
  { region: 'Asia', list: [['CN', 'China'], ['JP', 'Japan'], ['KR', 'South Korea'], ['SG', 'Singapore'], ['HK', 'Hong Kong'], ['TW', 'Taiwan'], ['AE', 'UAE'], ['SA', 'Saudi Arabia'], ['ID', 'Indonesia'], ['MY', 'Malaysia'], ['TH', 'Thailand'], ['VN', 'Vietnam'], ['PH', 'Philippines'], ['PK', 'Pakistan'], ['BD', 'Bangladesh'], ['IL', 'Israel'], ['TR', 'Turkey'], ['IN', 'India']] },
  { region: 'Europe', list: [['GB', 'United Kingdom'], ['DE', 'Germany'], ['FR', 'France'], ['NL', 'Netherlands'], ['RU', 'Russia'], ['IE', 'Ireland'], ['ES', 'Spain'], ['IT', 'Italy'], ['SE', 'Sweden'], ['PL', 'Poland'], ['CH', 'Switzerland'], ['FI', 'Finland']] },
  { region: 'Americas', list: [['US', 'United States'], ['CA', 'Canada'], ['MX', 'Mexico'], ['BR', 'Brazil'], ['AR', 'Argentina'], ['CL', 'Chile'], ['CO', 'Colombia']] },
  { region: 'Africa & Oceania', list: [['ZA', 'South Africa'], ['NG', 'Nigeria'], ['KE', 'Kenya'], ['EG', 'Egypt'], ['AU', 'Australia'], ['NZ', 'New Zealand']] },
]

export const COUNTRY_NAME: Record<string, string> = Object.fromEntries(COUNTRY_GROUPS.flatMap(g => g.list))

export const MAX_COUNTRIES = 25

export function countryName(code: string): string {
  return COUNTRY_NAME[code] ?? code
}


/** Time zones of the countries Opskest can check from, to find the country this server runs in. */
const ZONE_COUNTRY: Record<string, string> = {
  'Asia/Kolkata': 'IN', 'Asia/Calcutta': 'IN', 'Asia/Shanghai': 'CN', 'Asia/Tokyo': 'JP', 'Asia/Seoul': 'KR', 'Asia/Singapore': 'SG', 'Asia/Hong_Kong': 'HK',
  'Asia/Taipei': 'TW', 'Asia/Dubai': 'AE', 'Asia/Riyadh': 'SA', 'Asia/Jakarta': 'ID', 'Asia/Kuala_Lumpur': 'MY', 'Asia/Bangkok': 'TH', 'Asia/Ho_Chi_Minh': 'VN',
  'Asia/Saigon': 'VN', 'Asia/Manila': 'PH', 'Asia/Karachi': 'PK', 'Asia/Dhaka': 'BD', 'Asia/Jerusalem': 'IL', 'Asia/Tel_Aviv': 'IL', 'Europe/Istanbul': 'TR',
  'Europe/London': 'GB', 'Europe/Berlin': 'DE', 'Europe/Paris': 'FR', 'Europe/Amsterdam': 'NL', 'Europe/Moscow': 'RU', 'Europe/Dublin': 'IE', 'Europe/Madrid': 'ES',
  'Europe/Rome': 'IT', 'Europe/Stockholm': 'SE', 'Europe/Warsaw': 'PL', 'Europe/Zurich': 'CH', 'Europe/Helsinki': 'FI',
  'America/New_York': 'US', 'America/Chicago': 'US', 'America/Denver': 'US', 'America/Los_Angeles': 'US', 'America/Phoenix': 'US', 'America/Anchorage': 'US',
  'America/Toronto': 'CA', 'America/Vancouver': 'CA', 'America/Mexico_City': 'MX', 'America/Sao_Paulo': 'BR', 'America/Argentina/Buenos_Aires': 'AR',
  'America/Santiago': 'CL', 'America/Bogota': 'CO', 'Africa/Johannesburg': 'ZA', 'Africa/Lagos': 'NG', 'Africa/Nairobi': 'KE', 'Africa/Cairo': 'EG',
  'Australia/Sydney': 'AU', 'Australia/Melbourne': 'AU', 'Australia/Brisbane': 'AU', 'Australia/Perth': 'AU', 'Australia/Adelaide': 'AU', 'Pacific/Auckland': 'NZ',
}

/**
 * The country this server runs in, as one of the checkable country codes, or
 * null when unknown. SERVER_REGION wins when it names a country (by name or
 * code); otherwise the server's display time zone decides.
 */
export function hostedCountry(region: string, timeZone: string): string | null {
  const r = region.trim().toLowerCase()
  if (r && r !== 'this server') {
    for (const g of COUNTRY_GROUPS) for (const [code, name] of g.list) {
      if (r === code.toLowerCase() || r === name.toLowerCase() || r.includes(name.toLowerCase())) return code
    }
  }
  return ZONE_COUNTRY[timeZone] ?? null
}
