package main

import (
	"net/url"
	"strings"
)

// Retailer is a merchant and the domains it trades on.
type Retailer struct {
	Name    string
	Domains []string
	// Search is the retailer's own site-search URL with {q} for the query. When
	// empty, a Google "site:" search link is used instead.
	Search string
}

// Region groups the retailers relevant to a country. Order matters: the best-known,
// most general retailers come first, because they are the first to be offered for
// manual lookup.
type Region struct {
	Code      string
	Name      string
	Currency  string
	Retailers []Retailer
}

// AnyRegion disables retailer filtering.
const AnyRegion = "ANY"

func r(name, domain string) Retailer { return Retailer{Name: name, Domains: []string{domain}} }

func rs(name, domain, search string) Retailer {
	return Retailer{Name: name, Domains: []string{domain}, Search: search}
}

var regions = []Region{
	{"AU", "Australia", "AUD", []Retailer{
		rs("Amazon AU", "amazon.com.au", "https://www.amazon.com.au/s?k={q}"),
		rs("eBay AU", "ebay.com.au", "https://www.ebay.com.au/sch/i.html?_nkw={q}"),
		rs("Kmart", "kmart.com.au", "https://www.kmart.com.au/search/?searchTerm={q}"),
		rs("Big W", "bigw.com.au", "https://www.bigw.com.au/search?q={q}"),
		rs("Target AU", "target.com.au", "https://www.target.com.au/search?text={q}"),
		rs("Woolworths", "woolworths.com.au", "https://www.woolworths.com.au/shop/search/products?searchTerm={q}"),
		rs("Coles", "coles.com.au", "https://www.coles.com.au/search/products?q={q}"),
		rs("Catch", "catch.com.au", "https://www.catch.com.au/search?query={q}"),
		rs("Kogan", "kogan.com", "https://www.kogan.com/au/shop/?q={q}"),
		rs("Officeworks", "officeworks.com.au", "https://www.officeworks.com.au/shop/officeworks/search?q={q}"),
		rs("JB Hi-Fi", "jbhifi.com.au", "https://www.jbhifi.com.au/search?query={q}"),
		rs("Harvey Norman", "harveynorman.com.au", "https://www.harveynorman.com.au/catalogsearch/result/?q={q}"),
		rs("Bunnings", "bunnings.com.au", "https://www.bunnings.com.au/search/products?q={q}"),
		rs("The Good Guys", "thegoodguys.com.au", "https://www.thegoodguys.com.au/SearchDisplay?searchTerm={q}"),
		rs("Chemist Warehouse", "chemistwarehouse.com.au", "https://www.chemistwarehouse.com.au/search?searchtext={q}"),
		rs("Priceline", "priceline.com.au", "https://www.priceline.com.au/search?q={q}"),
		rs("MyDeal", "mydeal.com.au", "https://www.mydeal.com.au/search?q={q}"),
		rs("Petbarn", "petbarn.com.au", "https://www.petbarn.com.au/search?q={q}"),
		rs("Petstock", "petstock.com.au", "https://www.petstock.com.au/search?q={q}"),
		rs("Pet Circle", "petcircle.com.au", "https://www.petcircle.com.au/search?q={q}"),
		rs("Myer", "myer.com.au", "https://www.myer.com.au/search?query={q}"),
		r("David Jones", "davidjones.com"),
		rs("Mwave", "mwave.com.au", "https://www.mwave.com.au/searchresult?w={q}"),
		rs("PC Case Gear", "pccasegear.com", "https://www.pccasegear.com/search?query={q}"),
		r("Scorptec", "scorptec.com.au"),
		r("Apple AU", "apple.com/au"),
	}},
	{"US", "United States", "USD", []Retailer{
		rs("Amazon", "amazon.com", "https://www.amazon.com/s?k={q}"),
		rs("Walmart", "walmart.com", "https://www.walmart.com/search?q={q}"),
		rs("Target", "target.com", "https://www.target.com/s?searchTerm={q}"),
		rs("Best Buy", "bestbuy.com", "https://www.bestbuy.com/site/searchpage.jsp?st={q}"),
		rs("eBay", "ebay.com", "https://www.ebay.com/sch/i.html?_nkw={q}"),
		rs("Costco", "costco.com", "https://www.costco.com/CatalogSearch?keyword={q}"),
		rs("Home Depot", "homedepot.com", "https://www.homedepot.com/s/{q}"),
		rs("Lowe's", "lowes.com", "https://www.lowes.com/search?searchTerm={q}"),
		rs("Newegg", "newegg.com", "https://www.newegg.com/p/pl?d={q}"),
		rs("Etsy", "etsy.com", "https://www.etsy.com/search?q={q}"),
		r("B&H", "bhphotovideo.com"),
		r("Macy's", "macys.com"),
		r("Nordstrom", "nordstrom.com"),
		r("GameStop", "gamestop.com"),
		r("Wayfair", "wayfair.com"),
		r("Sephora", "sephora.com"),
		r("Kohl's", "kohls.com"),
		rs("Chewy", "chewy.com", "https://www.chewy.com/s?query={q}"),
		r("Apple", "apple.com"),
	}},
	{"CA", "Canada", "CAD", []Retailer{
		rs("Amazon CA", "amazon.ca", "https://www.amazon.ca/s?k={q}"),
		rs("Walmart CA", "walmart.ca", "https://www.walmart.ca/search?q={q}"),
		rs("Best Buy CA", "bestbuy.ca", "https://www.bestbuy.ca/en-ca/search?search={q}"),
		rs("Canadian Tire", "canadiantire.ca", "https://www.canadiantire.ca/en/search-results.html?q={q}"),
		rs("Costco CA", "costco.ca", "https://www.costco.ca/CatalogSearch?keyword={q}"),
		rs("eBay CA", "ebay.ca", "https://www.ebay.ca/sch/i.html?_nkw={q}"),
		r("Home Depot CA", "homedepot.ca"),
		r("Newegg CA", "newegg.ca"),
		r("Staples CA", "staples.ca"),
		r("London Drugs", "londondrugs.com"),
		r("The Bay", "thebay.com"),
		r("Etsy", "etsy.com"),
		r("Shoppers Drug Mart", "shoppersdrugmart.ca"),
		r("Indigo", "indigo.ca"),
		r("Sport Chek", "sportchek.ca"),
		r("PetSmart CA", "petsmart.ca"),
		r("Pet Valu", "petvalu.ca"),
	}},
	{"GB", "United Kingdom", "GBP", []Retailer{
		rs("Amazon UK", "amazon.co.uk", "https://www.amazon.co.uk/s?k={q}"),
		rs("eBay UK", "ebay.co.uk", "https://www.ebay.co.uk/sch/i.html?_nkw={q}"),
		rs("Argos", "argos.co.uk", "https://www.argos.co.uk/search/{q}/"),
		rs("Currys", "currys.co.uk", "https://www.currys.co.uk/search?q={q}"),
		rs("John Lewis", "johnlewis.com", "https://www.johnlewis.com/search?search-term={q}"),
		rs("Tesco", "tesco.com", "https://www.tesco.com/groceries/en-GB/search?query={q}"),
		r("Asda", "asda.com"),
		r("Sainsbury's", "sainsburys.co.uk"),
		r("Very", "very.co.uk"),
		r("AO", "ao.com"),
		r("B&Q", "diy.com"),
		r("Boots", "boots.com"),
		r("Screwfix", "screwfix.com"),
		r("Halfords", "halfords.com"),
		r("Pets at Home", "petsathome.com"),
		r("Scan", "scan.co.uk"),
		r("Overclockers UK", "overclockers.co.uk"),
		r("Etsy", "etsy.com"),
	}},
	{"NZ", "New Zealand", "NZD", []Retailer{
		r("Noel Leeming", "noelleeming.co.nz"),
		r("The Warehouse", "thewarehouse.co.nz"),
		r("PB Tech", "pbtech.co.nz"),
		r("Trade Me", "trademe.co.nz"),
		r("Mighty Ape", "mightyape.co.nz"),
		r("Woolworths NZ", "woolworths.co.nz"),
		r("Bunnings NZ", "bunnings.co.nz"),
		r("Harvey Norman NZ", "harveynorman.co.nz"),
		r("Kmart NZ", "kmart.co.nz"),
		r("JB Hi-Fi NZ", "jbhifi.co.nz"),
		r("Mitre 10", "mitre10.co.nz"),
		r("Briscoes", "briscoes.co.nz"),
		r("Farmers", "farmers.co.nz"),
		r("Chemist Warehouse NZ", "chemistwarehouse.co.nz"),
		r("Torpedo7", "torpedo7.co.nz"),
		r("Animates", "animates.co.nz"),
	}},
	{"DE", "Germany", "EUR", []Retailer{
		r("Amazon DE", "amazon.de"), r("eBay DE", "ebay.de"), r("OTTO", "otto.de"), r("MediaMarkt", "mediamarkt.de"),
		r("Saturn", "saturn.de"), r("Zalando", "zalando.de"), r("Kaufland", "kaufland.de"), r("Cyberport", "cyberport.de"),
		r("Notebooksbilliger", "notebooksbilliger.de"), r("Alternate", "alternate.de"), r("Galaxus", "galaxus.de"),
		r("Conrad", "conrad.de"), r("OBI", "obi.de"), r("dm", "dm.de"), r("Fressnapf", "fressnapf.de"), r("Zooplus", "zooplus.de"),
		r("Etsy", "etsy.com"),
	}},
	{"FR", "France", "EUR", []Retailer{
		r("Amazon FR", "amazon.fr"), r("Fnac", "fnac.com"), r("Cdiscount", "cdiscount.com"), r("Darty", "darty.com"),
		r("Boulanger", "boulanger.com"), r("LDLC", "ldlc.com"), r("Leroy Merlin", "leroymerlin.fr"), r("eBay FR", "ebay.fr"),
		r("Carrefour", "carrefour.fr"), r("Auchan", "auchan.fr"), r("E.Leclerc", "e.leclerc"), r("Decathlon", "decathlon.fr"),
		r("Conforama", "conforama.fr"), r("Rue du Commerce", "rueducommerce.fr"), r("Zooplus", "zooplus.fr"),
	}},
	{"ES", "Spain", "EUR", []Retailer{
		r("Amazon ES", "amazon.es"), r("PcComponentes", "pccomponentes.com"), r("El Corte Inglés", "elcorteingles.es"),
		r("MediaMarkt ES", "mediamarkt.es"), r("Fnac ES", "fnac.es"), r("eBay ES", "ebay.es"), r("Worten", "worten.es"),
		r("Carrefour ES", "carrefour.es"), r("Leroy Merlin ES", "leroymerlin.es"), r("Decathlon ES", "decathlon.es"),
		r("Conforama ES", "conforama.es"), r("Zooplus ES", "zooplus.es"), r("Alcampo", "alcampo.es"), r("AliExpress", "aliexpress.com"),
		r("Miravia", "miravia.es"),
	}},
	{"IT", "Italy", "EUR", []Retailer{
		r("Amazon IT", "amazon.it"), r("MediaWorld", "mediaworld.it"), r("Unieuro", "unieuro.it"), r("ePRICE", "eprice.it"),
		r("eBay IT", "ebay.it"), r("Euronics", "euronics.it"), r("Leroy Merlin IT", "leroymerlin.it"), r("Decathlon IT", "decathlon.it"),
		r("ManoMano", "manomano.it"), r("Zooplus IT", "zooplus.it"), r("Trony", "trony.it"), r("Esselunga", "esselunga.it"),
		r("Carrefour IT", "carrefour.it"), r("AliExpress", "aliexpress.com"), r("Cdiscount IT", "cdiscount.it"),
	}},
	{"NL", "Netherlands", "EUR", []Retailer{
		r("Amazon NL", "amazon.nl"), r("bol", "bol.com"), r("Coolblue", "coolblue.nl"), r("MediaMarkt NL", "mediamarkt.nl"),
		r("eBay NL", "ebay.nl"), r("Wehkamp", "wehkamp.nl"), r("Alternate NL", "alternate.nl"), r("Azerty", "azerty.nl"),
		r("Gamma", "gamma.nl"), r("Karwei", "karwei.nl"), r("Hornbach NL", "hornbach.nl"), r("Zooplus NL", "zooplus.nl"),
		r("Kruidvat", "kruidvat.nl"), r("Albert Heijn", "ah.nl"), r("Decathlon NL", "decathlon.nl"),
	}},
	{"IN", "India", "INR", []Retailer{
		r("Amazon IN", "amazon.in"), r("Flipkart", "flipkart.com"), r("Croma", "croma.com"), r("Reliance Digital", "reliancedigital.in"),
		r("Myntra", "myntra.com"), r("Tata CLiQ", "tatacliq.com"), r("Snapdeal", "snapdeal.com"), r("Meesho", "meesho.com"),
		r("Ajio", "ajio.com"), r("JioMart", "jiomart.com"), r("Vijay Sales", "vijaysales.com"), r("Nykaa", "nykaa.com"),
		r("FirstCry", "firstcry.com"), r("BigBasket", "bigbasket.com"), r("Poorvika", "poorvika.com"),
	}},
	{"SG", "Singapore", "SGD", []Retailer{
		r("Amazon SG", "amazon.sg"), r("Lazada", "lazada.sg"), r("Shopee", "shopee.sg"), r("Courts", "courts.com.sg"),
		r("Challenger", "challenger.sg"), r("Best Denki", "bestdenki.com.sg"), r("Harvey Norman SG", "harveynorman.com.sg"),
		r("FairPrice", "fairprice.com.sg"), r("Qoo10", "qoo10.sg"), r("IKEA SG", "ikea.com/sg"), r("Guardian SG", "guardian.com.sg"),
		r("Watsons SG", "watsons.com.sg"), r("Decathlon SG", "decathlon.sg"), r("Zalora SG", "zalora.sg"), r("Sephora SG", "sephora.sg"),
	}},
	{"JP", "Japan", "JPY", []Retailer{
		r("Amazon JP", "amazon.co.jp"), r("Rakuten", "rakuten.co.jp"), r("Yodobashi", "yodobashi.com"), r("Bic Camera", "biccamera.com"),
		r("Kakaku", "kakaku.com"), r("Yahoo Shopping", "shopping.yahoo.co.jp"), r("Joshin", "joshin.co.jp"), r("Edion", "edion.com"),
		r("Nojima", "nojima.co.jp"), r("Kojima", "kojima.net"), r("Yamada Denki", "yamada-denki.jp"), r("ZOZOTOWN", "zozo.jp"),
		r("Muji", "muji.com"), r("Uniqlo", "uniqlo.com/jp"), r("Mercari", "mercari.com"),
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

// matches reports whether u belongs to this retailer. Domains may carry a
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

// SearchURL is a link to this retailer's search for q — the page to open to look a
// product up by hand.
func (rt Retailer) SearchURL(q string) string {
	if rt.Search != "" {
		return strings.ReplaceAll(rt.Search, "{q}", url.QueryEscape(q))
	}
	dom, _, _ := strings.Cut(rt.Domains[0], "/")
	return "https://www.google.com/search?q=" + url.QueryEscape("site:"+dom+" "+q)
}

// Allows reports whether u is a retailer for the region.
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
