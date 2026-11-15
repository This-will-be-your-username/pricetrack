package main

import (
	"net/url"
	"strings"
)

// Retailer is a merchant and the domains it trades on.
type Retailer struct {
	Name    string
	Domains []string
}

// Region groups the retailers relevant to a country.
type Region struct {
	Code      string
	Name      string
	Currency  string
	Retailers []Retailer
}

// AnyRegion disables retailer filtering.
const AnyRegion = "ANY"

func r(name string, domains ...string) Retailer { return Retailer{name, domains} }

var regions = []Region{
	{"AU", "Australia", "AUD", []Retailer{
		r("Amazon AU", "amazon.com.au"), r("JB Hi-Fi", "jbhifi.com.au"), r("Harvey Norman", "harveynorman.com.au"),
		r("Bunnings", "bunnings.com.au"), r("Kmart", "kmart.com.au"), r("Target AU", "target.com.au"),
		r("Big W", "bigw.com.au"), r("Officeworks", "officeworks.com.au"), r("eBay AU", "ebay.com.au"),
		r("Catch", "catch.com.au"), r("Woolworths", "woolworths.com.au"), r("Coles", "coles.com.au"),
		r("Mwave", "mwave.com.au"), r("PC Case Gear", "pccasegear.com"), r("Scorptec", "scorptec.com.au"),
		r("The Good Guys", "thegoodguys.com.au"), r("Chemist Warehouse", "chemistwarehouse.com.au"),
		r("Myer", "myer.com.au"), r("David Jones", "davidjones.com"), r("Apple AU", "apple.com/au"),
	}},
	{"US", "United States", "USD", []Retailer{
		r("Amazon", "amazon.com"), r("Walmart", "walmart.com"), r("Target", "target.com"),
		r("Best Buy", "bestbuy.com"), r("eBay", "ebay.com"), r("Newegg", "newegg.com"),
		r("Costco", "costco.com"), r("Home Depot", "homedepot.com"), r("Lowe's", "lowes.com"),
		r("Etsy", "etsy.com"), r("B&H", "bhphotovideo.com"), r("Macy's", "macys.com"),
		r("Nordstrom", "nordstrom.com"), r("GameStop", "gamestop.com"), r("Wayfair", "wayfair.com"),
		r("Sephora", "sephora.com"), r("Kohl's", "kohls.com"), r("Apple", "apple.com"),
	}},
	{"CA", "Canada", "CAD", []Retailer{
		r("Amazon CA", "amazon.ca"), r("Walmart CA", "walmart.ca"), r("Best Buy CA", "bestbuy.ca"),
		r("Canadian Tire", "canadiantire.ca"), r("Costco CA", "costco.ca"), r("Home Depot CA", "homedepot.ca"),
		r("eBay CA", "ebay.ca"), r("Newegg CA", "newegg.ca"), r("Etsy", "etsy.com"),
		r("Staples CA", "staples.ca"), r("London Drugs", "londondrugs.com"), r("The Bay", "thebay.com"),
	}},
	{"GB", "United Kingdom", "GBP", []Retailer{
		r("Amazon UK", "amazon.co.uk"), r("Argos", "argos.co.uk"), r("Currys", "currys.co.uk"),
		r("John Lewis", "johnlewis.com"), r("eBay UK", "ebay.co.uk"), r("Tesco", "tesco.com"),
		r("Very", "very.co.uk"), r("Boots", "boots.com"), r("AO", "ao.com"), r("Screwfix", "screwfix.com"),
		r("Scan", "scan.co.uk"), r("Overclockers UK", "overclockers.co.uk"), r("Etsy", "etsy.com"),
	}},
	{"NZ", "New Zealand", "NZD", []Retailer{
		r("Noel Leeming", "noelleeming.co.nz"), r("The Warehouse", "thewarehouse.co.nz"), r("PB Tech", "pbtech.co.nz"),
		r("Mighty Ape", "mightyape.co.nz"), r("Woolworths NZ", "woolworths.co.nz"), r("Bunnings NZ", "bunnings.co.nz"),
		r("Harvey Norman NZ", "harveynorman.co.nz"), r("Trade Me", "trademe.co.nz"), r("Kmart NZ", "kmart.co.nz"),
		r("JB Hi-Fi NZ", "jbhifi.co.nz"), r("Mitre 10", "mitre10.co.nz"),
	}},
	{"DE", "Germany", "EUR", []Retailer{
		r("Amazon DE", "amazon.de"), r("OTTO", "otto.de"), r("MediaMarkt", "mediamarkt.de"), r("Saturn", "saturn.de"),
		r("eBay DE", "ebay.de"), r("Zalando", "zalando.de"), r("Notebooksbilliger", "notebooksbilliger.de"),
		r("Alternate", "alternate.de"), r("OBI", "obi.de"), r("Etsy", "etsy.com"),
	}},
	{"FR", "France", "EUR", []Retailer{
		r("Amazon FR", "amazon.fr"), r("Fnac", "fnac.com"), r("Cdiscount", "cdiscount.com"), r("Darty", "darty.com"),
		r("Boulanger", "boulanger.com"), r("LDLC", "ldlc.com"), r("Leroy Merlin", "leroymerlin.fr"),
		r("eBay FR", "ebay.fr"), r("Carrefour", "carrefour.fr"),
	}},
	{"ES", "Spain", "EUR", []Retailer{
		r("Amazon ES", "amazon.es"), r("PcComponentes", "pccomponentes.com"), r("El Corte Inglés", "elcorteingles.es"),
		r("MediaMarkt ES", "mediamarkt.es"), r("Fnac ES", "fnac.es"), r("eBay ES", "ebay.es"),
	}},
	{"IT", "Italy", "EUR", []Retailer{
		r("Amazon IT", "amazon.it"), r("MediaWorld", "mediaworld.it"), r("Unieuro", "unieuro.it"),
		r("ePRICE", "eprice.it"), r("eBay IT", "ebay.it"),
	}},
	{"NL", "Netherlands", "EUR", []Retailer{
		r("Amazon NL", "amazon.nl"), r("bol", "bol.com"), r("Coolblue", "coolblue.nl"),
		r("MediaMarkt NL", "mediamarkt.nl"), r("eBay NL", "ebay.nl"),
	}},
	{"IN", "India", "INR", []Retailer{
		r("Amazon IN", "amazon.in"), r("Flipkart", "flipkart.com"), r("Croma", "croma.com"),
		r("Reliance Digital", "reliancedigital.in"), r("Myntra", "myntra.com"), r("Tata CLiQ", "tatacliq.com"),
	}},
	{"SG", "Singapore", "SGD", []Retailer{
		r("Amazon SG", "amazon.sg"), r("Lazada", "lazada.sg"), r("Shopee", "shopee.sg"),
		r("Courts", "courts.com.sg"), r("Challenger", "challenger.sg"),
	}},
	{"JP", "Japan", "JPY", []Retailer{
		r("Amazon JP", "amazon.co.jp"), r("Rakuten", "rakuten.co.jp"), r("Yodobashi", "yodobashi.com"),
		r("Bic Camera", "biccamera.com"), r("Kakaku", "kakaku.com"),
	}},
}

// FindRegion returns the region with the given code.
func FindRegion(code string) (Region, bool) {
	for _, rg := range regions {
		if rg.Code == code {
			return rg, true
		}
	}
	return Region{}, false
}

// matches reports whether rawURL belongs to this retailer. Domains may carry a
// path prefix (e.g. "apple.com/au").
func (rt Retailer) matches(u *url.URL) bool {
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	for _, d := range rt.Domains {
		dom, path, _ := strings.Cut(d, "/")
		if host != dom && !strings.HasSuffix(host, "."+dom) {
			continue
		}
		if path == "" || strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), path) {
			return true
		}
	}
	return false
}

// Allows reports whether rawURL is a retailer for the region.
func (rg Region) Allows(u *url.URL) bool {
	for _, rt := range rg.Retailers {
		if rt.matches(u) {
			return true
		}
	}
	return false
}

// retailerName looks a friendly name up across every region.
func retailerName(u *url.URL) string {
	for _, rg := range regions {
		for _, rt := range rg.Retailers {
			if rt.matches(u) {
				return rt.Name
			}
		}
	}
	return ""
}
