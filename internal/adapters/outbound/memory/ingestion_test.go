package memory_test

import (
	"meal-recommendation/internal/adapters/outbound/memory"
	"meal-recommendation/internal/testsupport/offeringestion"
	"testing"
	"time"
)

func TestIngestion(t *testing.T) {
	o := memory.NewOfferRepository(time.Now, time.Hour)
	p := memory.NewProductRepository()
	offeringestion.Run(t, memory.NewIngestionStore(o, p), o, p, o)
}
