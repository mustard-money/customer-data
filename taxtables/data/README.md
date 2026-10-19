# Tax data

US income and payroll tax parameters by year, transcribed from official
sources. The `taxtables` package embeds these files, validates them, projects
future years (see [Projection](#projection)) and computes taxes with them.
The customer-data server imports it, loads the tables into Postgres at
startup and computes taxes for signed-in customers; the tables are never
sent to clients (the app has no copy).

Government publications and public-domain / CC0 data come first (IRS, SSA,
CRS, state revenue departments and legislatures, the Advisory Commission on
Intergovernmental Relations' "Significant Features of Fiscal Federalism"
editions, the PSL Tax-Calculator's CC0 parameters), read live, from the NBER
historical state tax forms archive, or as Wayback Machine copies of the same
government documents. Every year cites the document its figures were checked
against.

Secondary sources fill gaps those documents leave: Tax Foundation tables
(including its "Facts and Figures on Government Finance", whose local tables
cite Commerce Clearing House), Federation of Tax Administrators tables,
university policy papers and news reports. A secondary source never replaces
a year a government document supports. Every year that rests on one has
`notes` beginning with `Secondary source (` so the set can be listed or
dropped in one pass; the coverage section lists them. Two local start years
(`since`) also rest on news reports: Aurora 1986 and some Indiana counties'
1973 or 1974 (see their notes). Commercial tax services (CCH, Prentice-Hall)
aren't used directly.

The ACIR editions (scans at library.unt.edu/gpo/ACIR/) print every
state's full rate schedule as of December 31, 1969, 1970 and 1971, July 1,
1973 and 1976, July 1, 1980 and a date in each of 1983-1994, with exemptions
and standard deductions in companion tables, and (from 1970) a table or
footnotes of selected cities' and counties' local income tax rates. They're
the source for most of the 1970s-1990s state years that no state document
covers and for the pre-2000 rates of most local jurisdictions. Their limits:
no edition for 1972, 1974-1975, 1977-1979, 1981-1982 or 1995; states that
indexed their brackets are sometimes printed at the statutory base or the
previous year's amounts (such years were skipped); joint schedules are often
summarized as a range (years without a usable joint schedule were skipped
where the state's joint schedule differed); the local tables are compiled
from a commercial service and were found wrong for Indiana counties, so they
were used only for cities, and only where a city's own documents didn't
contradict them; and the scans are OCR'd, so figures were read from the
page images.

Other government scans that filled gaps: the Internet Archive's digitized
state-documents microfiche series (items `micro_IA4070…`, 1978-1995: revenue
departments' annual reports, which print the year's schedule for Arizona,
Iowa, Oklahoma and California, California's 1978–1982 index factors (those
years' brackets are computed from them and checked against the amounts the
reports print), California 1985's factor (computed from the 1984 schedule
with the 4.6% the FTB's 1987 report gives, which carries 1984 onto the
published 1986 schedule exactly) and South Carolina's 1985 and 1989 indexing); the Idaho Tax Commission's administrative-
rules archive (adminrules.idaho.gov), which prints that state's indexed
brackets for every year; the New Mexico Compilation Commission's historical
NMSA editions (nmonesource.com, 1989 onward); Maine's Law and Legislative
Digital Library (session laws by chapter); the South Carolina State
Library's scanned Acts volumes (their subject indexes establish which years
amended the rate section); Ohio's Table LG-11 municipal rate files back to
1983 on the Wayback Machine; and Wayback copies of revenue departments'
prior-year forms (Iowa 1995, California 1993, Maine 1999, Indiana 1996, and
Indiana's Form IT-40P booklet, whose chart gives every county's rate for
1992-1996); Maine's September Tax Alerts (1996-1998 schedules); county
and city codes' amendment histories; and the official state codes' history
notes (LexisNexis public-access editions), which show that a year with no
amendment to the rate, deduction or exemption sections kept the previous
law (Arkansas 1972–1982, Mississippi 1972); and HathiTrust's full-view
scans of state session laws (New Mexico's Laws 1974, ch. 88, for 1974–1976;
Kentucky's 1972 and 1976 Acts, for 1972 and 1974–1975; New York's 1968, 1970
and 1977 Laws, for 1970–1972 and 1977); Montana's statutory indexing rule
(MCA 1997, 15-30-101, -103 and -112: brackets times June CPI over June 1980
CPI, rounded to $100, exemptions to $10), which reproduces the published
1982, 1984, 1986, 1988 and 1990–1992 schedules and gives 1981, 1985 and
1989 (1989 also matches ACIR apart from one misprinted bracket); and the SC
Department of Revenue's 2019 Form SC4972, which prints the 2019 rate
schedule.

## Files

- `jurisdictions/<id>.json`: one taxing jurisdiction and its tax by year.
- `payroll.json`: Social Security and Medicare parameters by year.
- `capital-gains.json`: the federal treatment of long-term capital gains and
  qualified dividends, and the net investment income tax, by year.
- `zips.json`: which ZIP codes each state and local jurisdiction covers.

### Jurisdiction files

```json
{
  "id": "us-federal",
  "kind": "federal",
  "region": "California",
  "state": "CA",
  "years": {
    "1975": {
      "type": "brackets",
      "schedules": {
        "single": [{ "from": 0, "rate": 0.14 }, { "from": 500, "rate": 0.15 }],
        "marriedJoint": [{ "from": 0, "rate": 0.14 }, { "from": 1000, "rate": 0.15 }]
      },
      "deduction": { "single": 750, "marriedJoint": 1500 },
      "percentStandardDeduction": {
        "rate": 0.16,
        "minimum": { "single": 1600, "marriedJoint": 1900 },
        "maximum": { "single": 2300, "marriedJoint": 2600 }
      },
      "source": "https://www.irs.gov/pub/irs-prior/i1040--1975.pdf",
      "notes": "Optional free text: credits, surcharges and other rules not modelled."
    }
  }
}
```

- `id`: `us-federal`, `us-state-XX` (postal code) or `us-local-<slug>`.
- `kind`: `federal`, `state` or `local`. `region` (shown as-is in every
  language) is required for states and locals; `state` (postal code) for
  locals and states.
- `years`: keyed by tax year (4 digits). A missing year means it hasn't
  been transcribed (no allowed source found yet), not that there was no tax:
  the API lists such states under `missing` and such local jurisdictions
  under `missingLocals`. So record a year with no tax as
  `{ "type": "none" }` (a repealed local tax too: see Aurora from 2025).
- `since` (local jurisdictions, optional): the first year the tax was levied,
  when a source establishes it. Years before it with no row had no tax and
  aren't reported as missing; years from it on with no row are missing.
  Without `since`, every year with no row is missing, which is right for a
  local tax older than its transcribed history (New York City's began in
  1966; the data starts in 1985).
- `type`:
  - `brackets`: marginal `schedules` for `single` and, when it differs,
    `marriedJoint` (married filing jointly). Brackets ascend by `from`
    (taxable income where the rate starts); the first starts at 0. Rates are
    fractions (0.22 = 22%). Schedules are transcribed as published: in
    1977–1986 the federal schedules have the zero bracket amount built in as
    a 0% first bracket.
  - `flat`: one `rate` on all taxable income.
  - `fixed`: an `amount` `per` `month` or `year`, owed when earnings in the
    period reach `minimumEarnings` (e.g. Denver's occupational privilege tax).
  - `none`: no tax on wages that year.
- `deduction` (brackets and flat; optional): fixed amounts subtracted from
  income before the schedule, by filing status: the standard deduction when
  it's a fixed amount, plus personal exemptions (one for single, two for a
  married couple). Amounts that phase out or depend on income are left out and
  described in `notes`.
- `percentStandardDeduction` (optional): a standard deduction that's a share
  of income (`rate` × income, clamped to `minimum`..`maximum` by filing
  status), on top of `deduction`. Federal 1970–1976.
- `source`: URL of the document the year was checked against. `notes`:
  anything not modelled (surcharges, credits, maximum tax, phase-outs).

### `payroll.json`

```json
{
  "years": {
    "1990": {
      "socialSecurityRate": 0.062,
      "wageBase": 51300,
      "medicareRate": 0.0145,
      "medicareWageBase": 51300,
      "additionalMedicareRate": 0,
      "source": "https://www.ssa.gov/oact/cola/cbb.html"
    }
  }
}
```

Employee shares (the self-employed pay both halves). `medicareWageBase` is
omitted from 1994, when the cap was removed. `additionalMedicareRate` (0.009
from 2013) applies above `additionalMedicareThreshold` (`single`,
`marriedJoint`; not indexed).

### `capital-gains.json`

```json
{
  "years": {
    "1975": { "method": "exclusion", "excludedShare": 0.5, "alternativeRate": 0.25,
              "alternativeLimit": { "single": 50000, "marriedJoint": 50000 }, "source": "https://..." },
    "1991": { "method": "maxRate", "maxRate": 0.28, "source": "https://..." },
    "2005": { "method": "brackets", "tiers": [{ "rate": 0.05, "from": 0 }, { "rate": 0.15, "fromOrdinaryRate": 0.25 }],
              "source": "https://..." },
    "2025": { "method": "brackets",
              "schedules": { "single": [{ "from": 0, "rate": 0 }, { "from": 48350, "rate": 0.15 }, { "from": 533400, "rate": 0.2 }],
                             "marriedJoint": [{ "from": 0, "rate": 0 }, { "from": 96700, "rate": 0.15 }, { "from": 600050, "rate": 0.2 }] },
              "netInvestmentIncomeTax": { "rate": 0.038, "threshold": { "single": 200000, "marriedJoint": 250000 } },
              "source": "https://www.irs.gov/pub/irs-drop/rp-24-40.pdf" }
  }
}
```

Net long-term gain (and, from 2003, qualified dividends: `QualifiedDividendsFrom`
in `capitalgains.go`; before then the server taxes dividends as ordinary
income) is stacked on top of ordinary taxable income, as on the IRS Qualified Dividends and Capital Gain
Tax Worksheet (`FederalTax` in `capitalgains.go`). `method`:

- `exclusion` (1970–1986): `excludedShare` of the gain isn't income (50%, then
  60% from 1979). The alternative tax, where it applied, taxes the gain at
  `alternativeRate` up to `alternativeLimit` (no limit when absent, as in
  1981) and beyond it at `alternativeExcessRate` (1970–1971) or as the
  included share at ordinary rates; the lower of the two computations wins.
- `maxRate` (1987, 1991–1996): ordinary rates, none above `maxRate`.
- `ordinary` (1988–1990): no preference.
- `brackets`: preferential rates by where the gain falls in taxable income,
  either `tiers` tied to the ordinary brackets (1997–2017: a tier starts where
  the ordinary bracket taxed at `fromOrdinaryRate` starts; validated against
  that year's federal schedules) or dollar `schedules` (2009, 2018 on).

`netInvestmentIncomeTax` (from 2013) is 3.8% of the lesser of investment
income and modified AGI over the threshold (fixed in law, not indexed). States
and localities tax gains as ordinary income (the server adds them to each
state and local layer's income); state exclusions and capital gains taxes
(e.g. Washington's) aren't modelled. Every year is from a government
document: Joint Committee on Taxation bluebooks (1969 and 1978 Acts), the
IRS's Schedule D forms and Form 1040 instructions (1979–2017), and the yearly
inflation Revenue Procedures (2018 on, cross-checked with Tax-Calculator's
CG_brk parameters). Not modelled (noted per year): the minimum tax on
preferences, the maximum tax on earned income, the 28% collectibles and 25%
unrecaptured §1250 rates, the 2001 five-year 8%/18% rates, the dividend
exclusion before 1987, and the $25,000 alternative-tax limit for married
filing separately. Where the law changed mid-year (1978, 1997, 2003) the rule
covering most of the year is used and the other described in `notes`.

## Projection

Years after a jurisdiction's last transcribed year are projected, through
2100, the way the IRS has indexed federal brackets since 2018: every bracket
start and fixed deduction grows with an assumed chained CPI-U of 2.3% a year
from the last transcribed year, rounded down to a multiple of $50. Rates,
flat and fixed taxes stay as they are. The Social Security wage base grows at
the same rate, rounded down to a multiple of $300 as SSA rounds it.
Capital gains thresholds given in dollars are indexed like the brackets;
tiers tied to the ordinary brackets follow those brackets, and the net
investment income tax thresholds stay fixed, as in law.
Projected years are marked `projected` in the database and the API.
State and local brackets are projected the same way even where the state
doesn't index them, so far-off years are only indicative.

## Coverage (as of 2026-10-18)

Federal and capital gains: every year 1970–2026. Payroll: every year
1970–2027 (SSA published the 2027 wage base on October 14, 2026; the IRS
hadn't yet published its 2027 brackets, so 2027 income tax is projected).

States: 2,730 of 2,754 state-years 1970–2023 (98%), plus every state for
2024–2026. Years still to transcribe (no source found yet; the API lists
these states under `missing`):

| State | Missing years |
|---|---|
| ME | 1983–1987, 1990–1993 |
| NM | 1977–1985 |
| UT | 1973–1975 |
| VT | 1972, 1977, 1979 |

Why they're missing, by state: the session laws that would bridge the years
aren't online and the open compilations (The Book of the States, Facts and
Figures) print only each state's lowest and highest bracket (NM 1977–1985, whose joint schedule appears in no online document between
the 1974 session laws and the 1989 statute compilation (the 1978, 1981 and
1983 session laws are search-only on HathiTrust), and whose base after the
1977 federal zero bracket amount no source states, VT 1972, 1977 and 1979); brackets were indexed yearly and no source prints that year's full
indexed schedule (ME 1983–1987 and 1990); Maine
1991–1993 used temporary tables whose indexed lower brackets were never
printed (and a 1991–1992 surcharge the format can't express); or the state's own
documents contradict each other (UT 1973–1975).

Years resting on a secondary source (notes begin `Secondary source (`):

- Indiana: 1973 (no tax) for Adams, Daviess, Delaware, Jackson, Marion,
  Miami and Montgomery counties; Kosciusko 1974–1977, 1979–1984 and 1991;
  Putnam 1987–1991
- Cincinnati: 1972
- Lexington: 1980–1981, 1985
- Louisville: 1981
- Pittsburgh: 1972, 1974–1975, 1977–1978, 1980
- Scranton: 1975, 1981
- Youngstown: 1981–1982
- South Carolina: 2006–2008, 2012–2013, 2016–2018
- Vermont: 1978

Local jurisdictions: every one has years before 2024, back as far as each
government's publications go; a year with no row is listed by the API under
`missingLocals` unless it's before the tax began (`since`).

| Jurisdictions | History |
|---|---|
| Philadelphia | 1970–2026, from the city's wage tax rate history |
| New York City | 1978–2026 (NYC's tax dates from 1966; DTF booklets start in 1985 and its yearly memoranda extend the schedule back to 1978; no open source prints the 1970–1977 schedule) |
| Yonkers | 1984–2026, the surcharge's whole life |
| Maryland, 23 counties and Baltimore City | 1999–2026; before 1999 the local tax was a percentage of state tax, which the format can't express |
| Indiana, 92 counties | 1992–2026 for every county (1992–1996 from the DOR's Form IT-40P chart, then its yearly charts); 1973–1991 only for DeKalb, Elkhart, Lawrence, Owen, Spencer, White and Allen (county codes, a city budget) and Kosciusko and Putnam (newspapers), with 1973 recorded as no tax for seven counties whose councils rejected it; `since` set where the start year is documented; Jefferson County levied the tax in 1992, dropped it 1993–2003 and resumed in 2004 |
| Michigan, 24 cities | 1% cities from adoption (1970 onward); Detroit 1970–2026 (1981 at the year-end rate); Highland Park all but 1982 (the start of its 2% rate is disputed); Benton Harbor from 2018 |
| Ohio, 8 cities | All eight 1970–2026 (Cincinnati 1972 and Youngstown 1981–1982 from newspapers); from the cities' codes and rate histories, ACIR, city financial reports and the Department of Taxation's LG-11 tables (1983 onward) |
| Pennsylvania: Erie, Harrisburg, Reading, Allentown | 1970–2026, from the cities' ordinances, ACIR and DCED's municipal statistics; Scranton 1970–1971, 1973, 1975, 1981–1982 and 1988–2026; Pittsburgh 1970–1980 and 1985–2026 (1981–1984 left out: the city's reports, newspapers and the Tax Foundation give different city or school-district shares) |
| Louisville, Lexington, Birmingham, St. Louis, Kansas City | Louisville 1970–1971, 1973, 1976, 1979, 1981, 1984–1994 and 2003–2026; Lexington 1970–1971, 1973–1981, 1985–1989, 1991–1994 and 2001–2026 (its 0.5% school tax from March 1986); Birmingham 1970–2026 (tax began December 1970); St. Louis 1970–2026; Kansas City 1970–2026 |
| Colorado occupational privilege taxes | Denver 1970–2026 (tax began 1969); Aurora 2000–2024 (began 1986 per news reports; repealed 2025); Glendale 2004–2026 (began 2004); Greenwood Village 1999–2026 (began 1980); Sheridan 1987–2026 |
| Portland Arts, Metro SHS, Multnomah PFA | From their first years (2012, 2021, 2021) |
| Wilmington, DE | 1973–1976, 1983–1994, 2003 and 2005–2026 (1970–1972 was a stepped whole-income rate the format can't express) |

## Known approximations

Each is also stated in the year's `notes`. Where a document was ambiguous
the year was left out instead; these are the places where a judgement was
made:

- Rates read from a document dated inside the year (an ACIR edition's
  "as of" date, usually December 31 or mid-year; Ohio's LG-11 "as of
  December 31"; Facts and Figures' April 1 or September 1) are recorded for
  the whole tax year.
- Flat tax rates recorded for a calendar year when the rate changed mid-year:
  Philadelphia uses the average of the two half-year rates (as 2024–2026 do);
  Ohio cities (Canton 1981–1982 among them), Pennsylvania cities, Detroit
  1981, Saginaw 1989, Grand Rapids 2010, Kansas City 1971, Denver 1988 and
  Birmingham 1970 (the tax began December 1) use the year-end rate; Detroit
  2000–2003 are blended calendar-year rates derived from the statutory
  schedule; Indiana counties use the DOR's prorated calendar-year rates.
  Youngstown 1981–1982, where no document dates the increase, were left out.
- Maine 1978, when the schedule changed on July 1 and the statute prorated
  the year by days, records day-weighted rates for the two brackets that
  changed.
- Schedules carried across years with no edition or form of their own, on the
  strength of a statute or code history showing no amendment (Kentucky
  1977–1982, Louisiana 1977–1982, Oklahoma 1971–1978, North Carolina
  1977–1982, Arizona 1970–1975, 1977–1979 and 1981–1982, Utah 1977–1979 and
  1981–1982, Missouri 1970–1972, Delaware 1970, South Carolina 1972–1982,
  Mississippi 1972 and 1974–1982, Arkansas 1972–1982 and 1995–1996 (1995–1996
  from the Acts index rather than a code history line), New Mexico 1972 and 1987–1993; Detroit 1972–1980, Highland
  Park 1972–1981 and 1984–1994, Allentown, Erie, Harrisburg and Reading
  1972–1985, Louisville 1988 and 1990, Lexington 1974–1975 and 1977–1978,
  Kansas City 1977–1985, 1988 and 1990, Denver 1980–1987, Wilmington
  1974–1975 and 1990, Benton Harbor 2022, Indiana counties from their codes'
  enactment lists, Akron, Canton, Cincinnati and Youngstown 1970s from their
  codes' narrated histories) or of adjacent identical sources alone
  (Michigan 1% cities 2008–2023 cite the statute rather than each year's
  forms; Louisville 2009–2022, Lexington 1980, Denver 1972, 1974–1975 and
  1977–1978, Kansas City 1972 and 1974–1975, Maine 1994–1995 (the Legislature's
  fiscal office says brackets and exemption weren't adjusted for 1992–1997,
  so the printed 1996 amounts apply), Aurora 2001–2005, 2007, 2009
  and 2011–2018, Greenwood Village 2000–2002, 2004 and 2007–2011, Saginaw
  and Highland Park between their anchors).
- Ohio cities' intermediate years (Cleveland, Columbus, Dayton, Toledo,
  Cincinnati 1995–1999) rest on a rate history narrated in a city financial
  report or ordinance list plus ACIR anchors, not on a document for each
  year. New York City 1978–1984 rests on the Department of Taxation and
  Finance's yearly memoranda saying the 1985 schedule was continued.
- Pittsburgh and Allentown rows taken from DCED's municipal statistics read
  the listed municipal and school-district rates as stated Act 511 rates and
  halve their sum; the reading is confirmed by the cities' codes and
  financial reports for the years those exist. Scranton 1982 rests on an ACIR
  note that misprints Philadelphia's rate in the same sentence; it agrees
  with the 1981 figure.
- Joint-filer treatment stated by a secondary note rather than the schedule
  itself: Louisiana joint brackets double before 1986; New Mexico 1970–1973
  and Maine 1977–1982 joint brackets double per the ACIR editions' notes;
  Oklahoma 1970 joint brackets double per the ACIR note; California
  1983 single and 1988–1990 joint schedules are half and double the printed
  ones, per the statute's "twice the tax on half the income" rule.
- Arizona 1985 and 1987: the DOR reports print only the first and top
  brackets; the four between are multiples of the first, as the statute's
  single index factor and the printed neighbouring years imply.
- South Carolina 1997–2004 and 2015 come from the DOR's SC1040ES
  worksheets, which print the year's full schedule; they show that the Tax
  Foundation's and FTA's tables for those years lag one or two years.
  South Carolina 2007 records the statute's 0% bottom rate where the Tax
  Foundation table prints 2.5%; 2006 is dated by the 2006 federal deduction
  amounts printed beside its brackets.
- Lexington's 0.5% school tax rests on a Fayette County Public Schools notice
  read from a third-party copy; Denver 1988 assumes the $500 threshold took
  effect with the $5.75 rate (same ordinance).
- Louisville 1970–1971 and 1976–1979 are derived from the ACIR tables'
  footnotes (a city tax creditable against the county tax) rather than an
  itemized rate; Louisville 1992 keeps 2.2% although that year's ACIR table
  prints the county at 2.95% for residents, out of line with 1991 and 1994.
- Denver 1989–2020 rest on the pre-2020 code's history notes; two
  ordinances in that history (775-07 and 34-12) were not read and are
  assumed not to have changed the amount or threshold.
- Figures taken as printed although another source suggests otherwise:
  Montana 1987 brackets (identical to 1984 in the Taxpayers Digest; the
  statutory indexing rule gives $1,400 to $48,100 instead); Montana
  1990 omits a 5% education surtax the 1991 digest mentions; South Carolina
  1986 joint top bracket $13,600; California 1986 joint top bracket $57,580
  (the FTB's narrative says $57,590); Maine 1983–1984 standard deduction
  maxima; Maine 1980's low-income allowance isn't modelled; Mississippi
  1981–1984 standard deduction taken as the fixed $2,300 / $3,400 although
  ACIR 1983 prints "15%" with those maximums; Mississippi 1985–1996 record no
  deduction because the standard deduction after Laws 1985 ch. 411 isn't
  confirmed; Vermont 1980 keeps the Joint Fiscal Office's 23% although The
  Book of the States prints 25% as of January 1, 1980; New York City
  1978–1984 keep the extended 0.9–4.3% schedule although some ACIR and Facts
  and Figures tables print the statutory 0.4–2.0% base.
- Oklahoma 1970 leaves out the deduction of federal income tax, which the
  format can't express and which materially lowered the tax.
- Frederick County, MD (2023–2026) is recorded as marginal brackets; the
  booklet's wording may describe a whole-income step instead.
- Standard deductions that were a percentage of income are modelled with
  `percentStandardDeduction` (Arkansas and Arizona in the 1970s–1990s,
  North Carolina 1976–1988, federal 1970–1976).
- Nebraska 1987–1992 brackets come from the statute's reference to the 1987
  federal rate schedule rather than a printed Nebraska table.

Skipped on purpose: misprints (ACIR's Iowa 1989 rates and 1990 "$23,760",
Oklahoma's "$9,950" bracket end, Arizona 1986
deductions, Dayton 1994, Allentown 1988, Jefferson County KY 1983 "2.7%",
Scranton's inconsistent footnotes and the Indiana county tables; the IT-40P
chart's Adams 1994, Brown 1993 and Grant 1994; the Tax Foundation's 2019
South Carolina row), tables that reprinted a previous year's indexed amounts
(ACIR for Arizona, Maine and Montana 1985 and 1987, South Carolina 1985 and
1989; the Tax Foundation's January tables for South
Carolina 1997–2005 and 2015), Utah 1973–1975
(the state's documents conflict), Maine 1983–1987 and
1990 (the joint schedule was indexed separately and never printed) and
1991–1993 (temporary tables with unprinted indexed brackets), Highland Park 1982 and Pittsburgh 1981–1984 (sources give
different rates or city and school-district shares), Wilmington 1977–1982
(the city's regulations date its 1.25% rate to July 1978, while ACIR and the
Tax Foundation print 1.0% for 1978–1981), and Louisville 1995–2002 (the
county code's rate section
was amended in 1999 by an ordinance whose text isn't online).

The data.ca.gov FTB datasets print statutory rather than indexed amounts for
1974–1985 and 1987–1990 and base deductions for 1978–1994, and their 1992 row
duplicates 1991; they're reliable from 1993 (brackets) and 1995 (deductions).
The UNT file names of the 1983–1986 ACIR editions are shifted from the
tables' dates (SFFF-1982-1983.pdf holds the late-1983 tables, SFFF-1984.pdf
the late-1984 ones, SFFF-1985-1986.pdf October 1985, sfff1987.pdf October
1986); notes cite the file name.
