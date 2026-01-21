# Exemples concrets

## Exemple 1 : Produit enrichi (cas nominal)

### Input (TSV brut)

```tsv
id	title	description	image_link	price	brand
SKU789	basket nike	chaussure de sport	https://cdn.shop.com/nike-shoe.jpg	89.99 EUR	Nike
```

### Après Ingestion (normalized)

```json
{
  "product_id": "SKU789",
  "raw": {
    "id": "SKU789",
    "title": "basket nike",
    "description": "chaussure de sport",
    "image_link": "https://cdn.shop.com/nike-shoe.jpg",
    "price": "89.99 EUR",
    "brand": "Nike"
  },
  "normalized": {
    "offer_id": "SKU789",
    "title": "basket nike",
    "description": "chaussure de sport",
    "image_link": "https://cdn.shop.com/nike-shoe.jpg",
    "price": { "value": 89.99, "currency": "EUR" },
    "brand": "Nike",
    "attributes": {}
  }
}
```

### Après Audit

```json
{
  "hard": {
    "errors": [
      {
        "field": "title",
        "rule": "gmc_title_min_length",
        "message": "Title too short (11 chars, min 30)",
        "severity": "error"
      }
    ],
    "warnings": [
      {
        "field": "description",
        "rule": "gmc_description_quality",
        "message": "Description lacks detail (15 chars)",
        "severity": "warning"
      }
    ]
  },
  "smart": {
    "scores": {
      "clarity": 0.35,
      "completeness": 0.28,
      "agent_readiness": 0.31,
      "seo_quality": 0.40
    },
    "findings": [
      {
        "type": "missing_attributes",
        "details": "Missing: gender, color, size, product_type",
        "impact": "high"
      },
      {
        "type": "ambiguous_title",
        "details": "Title doesn't specify product type clearly",
        "impact": "medium"
      }
    ]
  }
}
```

### Retrieval (faits sourcés)

```json
{
  "queries": [
    {
      "query": "Nike basket specifications",
      "results": [
        {
          "url": "https://nike.com/product/SKU789",
          "title": "Nike Air Max 90 - Men's Running Shoes",
          "snippet": "The Nike Air Max 90 features a mesh upper, visible Air cushioning, and rubber outsole."
        }
      ]
    }
  ],
  "extracted_facts": [
    {
      "fact": "model: Air Max 90",
      "source": "https://nike.com/product/SKU789",
      "confidence": 0.95
    },
    {
      "fact": "product_type: running shoes",
      "source": "https://nike.com/product/SKU789",
      "confidence": 0.92
    },
    {
      "fact": "upper_material: mesh",
      "source": "https://nike.com/product/SKU789",
      "confidence": 0.88
    }
  ]
}
```

### Vision (observations)

```json
{
  "image_url": "https://cdn.shop.com/nike-shoe.jpg",
  "observations": [
    {
      "attribute": "color",
      "value": "white with black accents",
      "confidence": 0.94,
      "risk": "low"
    },
    {
      "attribute": "shoe_type",
      "value": "sneaker/running shoe",
      "confidence": 0.91,
      "risk": "low"
    }
  ]
}
```

### Proposals générées

```json
{
  "proposals": [
    {
      "id": "prop-001",
      "field": "title",
      "before": "basket nike",
      "after": "Nike Air Max 90 - Chaussures de running homme blanches",
      "rationale": [
        "Ajout du modèle exact (Air Max 90) - sourcé site officiel",
        "Précision du type de produit (running) - sourcé site officiel",
        "Ajout de la couleur (blanc) - confirmé par vision",
        "Ajout du genre (homme) - déduit de 'Men's' sur site officiel"
      ],
      "sources": [
        {
          "type": "web",
          "url": "https://nike.com/product/SKU789",
          "snippet": "Nike Air Max 90 - Men's Running Shoes"
        },
        {
          "type": "vision",
          "observation": "color: white",
          "confidence": 0.94
        }
      ],
      "confidence": 0.89,
      "risk": "low",
      "status": "proposed"
    },
    {
      "id": "prop-002",
      "field": "description",
      "before": "chaussure de sport",
      "after": "Chaussures de running Nike Air Max 90 pour homme. Upper en mesh respirant pour un confort optimal. Semelle avec technologie Air visible pour un amorti réactif.",
      "rationale": [
        "Expansion avec détails techniques sourcés",
        "Mention du matériau (mesh) - sourcé site officiel"
      ],
      "sources": [
        {
          "type": "web",
          "url": "https://nike.com/product/SKU789",
          "snippet": "features a mesh upper, visible Air cushioning"
        }
      ],
      "confidence": 0.85,
      "risk": "low",
      "status": "proposed"
    }
  ]
}
```

### Après Controller (validation)

```json
{
  "prop-001": {
    "approved": true,
    "checks": [
      { "check": "no_invention", "passed": true },
      { "check": "hard_rules_compliant", "passed": true },
      { "check": "sources_verified", "passed": true }
    ],
    "risk": "low"
  },
  "prop-002": {
    "approved": true,
    "checks": [
      { "check": "no_invention", "passed": true },
      { "check": "hard_rules_compliant", "passed": true },
      { "check": "sources_verified", "passed": true }
    ],
    "risk": "low"
  }
}
```

### Final (après accept)

```json
{
  "offer_id": "SKU789",
  "title": "Nike Air Max 90 - Chaussures de running homme blanches",
  "description": "Chaussures de running Nike Air Max 90 pour homme. Upper en mesh respirant pour un confort optimal. Semelle avec technologie Air visible pour un amorti réactif.",
  "image_link": "https://cdn.shop.com/nike-shoe.jpg",
  "price": "89.99 EUR",
  "brand": "Nike",
  "color": "white",
  "gender": "male",
  "product_type": "Running Shoes",
  "material": "mesh"
}
```

### Diff

```diff
- title: basket nike
+ title: Nike Air Max 90 - Chaussures de running homme blanches

- description: chaussure de sport  
+ description: Chaussures de running Nike Air Max 90 pour homme. Upper en mesh respirant pour un confort optimal. Semelle avec technologie Air visible pour un amorti réactif.

+ color: white
+ gender: male
+ product_type: Running Shoes
+ material: mesh
```

### Scores avant/après

| Métrique | Avant | Après | Delta |
|----------|-------|-------|-------|
| Clarity | 0.35 | 0.88 | +0.53 |
| Completeness | 0.28 | 0.82 | +0.54 |
| Agent Readiness | 0.31 | 0.85 | +0.54 |
| Hard Errors | 1 | 0 | -1 |

---

## Exemple 2 : Proposal rejetée (no-invention violation)

### Proposal problématique

```json
{
  "field": "title",
  "before": "veste outdoor",
  "after": "Veste outdoor imperméable coupe-vent avec capuche amovible",
  "rationale": ["Ajout de caractéristiques pour améliorer SEO"],
  "sources": [],
  "confidence": 0.72
}
```

### Controller rejet

```json
{
  "approved": false,
  "reason": "NO_INVENTION_VIOLATION",
  "details": [
    "Fait non sourcé: 'imperméable'",
    "Fait non sourcé: 'coupe-vent'",
    "Fait non sourcé: 'capuche amovible'"
  ],
  "risk": "high",
  "action": "Proposal bloquée. Sources requises pour ces caractéristiques."
}
```
