package config

// defaults returns the built-in configuration used when no config file is found.
// These are the seven reference dimensions from config.example.toml.
// The built-in defaults define the scoring philosophy but are not a hard constraint —
// users may define any dimensions they like in their config file.
func defaults() *Config {
	order := []string{
		"story", "enjoyment", "characters", "production", "pacing", "world_building", "value",
	}
	dims := map[string]DimensionDef{
		"story": {
			Label:         "Story",
			Description:   "Plot, hook, themes, and how well the narrative concludes",
			Weight:        0.25,
			BiasResistant: false,
		},
		"enjoyment": {
			Label:         "Enjoyment",
			Description:   "Gut feeling. How much fun did you have? Did you look forward to the next entry?",
			Weight:        0.15,
			BiasResistant: true,
		},
		"characters": {
			Label:         "Characters",
			Description:   "Relatability, growth arcs, and chemistry between the cast",
			Weight:        0.20,
			BiasResistant: false,
		},
		"production": {
			Label:         "Production",
			Description:   "Anime: animation fluidity, voice acting, OST. Manga: art style, character design, paneling",
			Weight:        0.15,
			BiasResistant: false,
		},
		"pacing": {
			Label:         "Pacing",
			Description:   "How well the story flows. Is it dragging? Rushed? Does it keep you hooked?",
			Weight:        0.10,
			BiasResistant: false,
		},
		"world_building": {
			Label:         "World Building",
			Description:   "The setting, rules/systems, and how immersive the lore feels",
			Weight:        0.10,
			BiasResistant: false,
		},
		"value": {
			Label:         "Value",
			Description:   "Rewatch/reread value. Does it have staying power in your mind?",
			Weight:        0.05,
			BiasResistant: true,
		},
	}

	genres := map[string]map[string]float64{
		"action": {
			"production":    1.4,
			"pacing":        1.3,
			"story":         0.8,
			"world_building": 0.9,
		},
		"adventure": {
			"world_building": 1.3,
			"pacing":        1.1,
			"story":         1.1,
		},
		"comedy": {
			"characters": 1.2,
			"pacing":     1.1,
			"story":      0.8,
			"world_building": 0.7,
		},
		"drama": {
			"story":      1.4,
			"characters": 1.3,
			"production": 0.8,
			"pacing":     1.1,
		},
		"fantasy": {
			"world_building": 1.5,
			"story":         1.1,
			"production":    1.1,
		},
		"horror": {
			"production": 1.3,
			"pacing":     1.2,
			"story":      1.1,
		},
		"mystery": {
			"story":         1.5,
			"pacing":        1.3,
			"world_building": 1.2,
		},
		"psychological": {
			"story":         1.4,
			"characters":    1.3,
			"pacing":        1.1,
			"world_building": 1.1,
			"production":    0.8,
		},
		"romance": {
			"characters": 1.4,
			"story":      1.1,
		},
		"sci-fi": {
			"world_building": 1.4,
			"story":         1.2,
			"production":    1.1,
		},
		"slice_of_life": {
			"characters":    1.4,
			"world_building": 0.7,
			"story":         0.8,
			"pacing":        0.9,
		},
		"sports": {
			"pacing":     1.3,
			"characters": 1.2,
			"production": 1.2,
			"story":      0.9,
			"world_building": 0.7,
		},
		"supernatural": {
			"world_building": 1.3,
			"story":         1.1,
			"production":    1.1,
		},
		"thriller": {
			"story":      1.4,
			"pacing":     1.4,
			"characters": 1.1,
			"production": 0.9,
		},
		"mecha": {
			"production":    1.4,
			"world_building": 1.2,
			"pacing":        1.1,
		},
	}

	return &Config{
		DimensionOrder:     order,
		Dimensions:         dims,
		Genres:             genres,
		MaxMultiplier:      DefaultMaxMultiplier,
		PrimaryGenreWeight: DefaultPrimaryGenreWeight,
		Server: ServerConfig{
			Port:               DefaultPort,
			CORSAllowedOrigins: []string{"http://localhost:3000", "http://localhost:5173", "http://localhost:8080"},
		},
	}
}
