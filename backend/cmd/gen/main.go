// Command gen generates typed GORM Gen query code from code-first model declarations.
package main

import (
	"log"

	"github.com/patrhez/agent-platform/backend/internal/model"
	"gorm.io/gen"
)

func main() {
	generator := gen.NewGenerator(gen.Config{
		OutPath: "internal/query",
		Mode:    gen.WithDefaultQuery | gen.WithQueryInterface | gen.WithGeneric,
	})
	generator.ApplyBasic(model.All()...)
	generator.Execute()
	log.Print("generated GORM query code")
}
