package domain

type Product struct {
	Name        string  `json:"name"`
	Price       float64 `json:"price"`
	Description string  `json:"description"`
	Rating      int     `json:"rating"`
	Reviews     int     `json:"reviews"`
	Link        string  `json:"link"`
}

type ScrapeResult struct {
	SourceURL     string    `json:"source_url"`
	ScrapedAt     string    `json:"scraped_at"`
	Total         int       `json:"total"`
	PagesVisited  int       `json:"pages_visited"`
	ErrorsSkipped int       `json:"errors_skipped"`
	DurationMS    int64     `json:"duration_ms"`
	Data          []Product `json:"data"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type HealthResponse struct {
	Status string `json:"status"`
}
