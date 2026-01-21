# Agent Reasoning - Exemple de trace

## Produit en entrée

```json
{
  "id": "SKU789",
  "title": "basket nike",
  "description": "chaussure de sport",
  "image_link": "https://cdn.shop.com/nike-shoe.jpg",
  "price": "89.99 EUR",
  "brand": "Nike"
}
```

---

## Trace d'exécution de l'agent

### Step 1 - Analyse initiale

**Thought**: "Je dois d'abord comprendre l'état de ce produit et identifier les problèmes."

**Tool call**: `analyze_product("SKU789")`

**Result**:
```json
{
  "gmc_compliance": {
    "valid": false,
    "errors": [
      { "field": "title", "issue": "Too short (11 chars, min 30)", "severity": "error" },
      { "field": "description", "issue": "Too short (17 chars, min 50)", "severity": "error" }
    ]
  },
  "quality_scores": {
    "title_quality": 0.25,
    "description_quality": 0.15,
    "completeness": 0.30,
    "agent_readiness": 0.28
  },
  "missing_attributes": ["color", "gender", "product_type", "material"],
  "improvement_opportunities": [
    { "field": "title", "current_issue": "Manque modèle, genre, type", "potential_action": "Rechercher nom complet du produit" },
    { "field": "description", "current_issue": "Aucun détail technique", "potential_action": "Ajouter specs si trouvées" }
  ]
}
```

---

### Step 2 - Recherche d'informations

**Thought**: "Le titre est trop court et manque de détails. Je vais chercher le nom complet du produit sur le site officiel Nike."

**Tool call**: `web_search("Nike basket SKU789 specifications", { site: "nike.com" })`

**Result**:
```json
{
  "results": [
    {
      "title": "Nike Air Max 90 - Men's Shoes",
      "url": "https://nike.com/air-max-90",
      "snippet": "The Nike Air Max 90 stays true to its OG running roots with the iconic Waffle sole, visible Air cushioning and..."
    }
  ]
}
```

---

### Step 3 - Extraction détaillée

**Thought**: "J'ai trouvé une page pertinente. Je vais extraire les détails techniques."

**Tool call**: `fetch_page("https://nike.com/air-max-90")`

**Result**:
```json
{
  "title": "Nike Air Max 90",
  "content": "Nike Air Max 90 Men's Shoes. The Nike Air Max 90 stays true to its OG running roots. Features: Leather and textile upper, Visible Max Air unit in heel, Rubber Waffle outsole, Foam midsole. Color: White/Black.",
  "structured_data": {
    "@type": "Product",
    "name": "Nike Air Max 90",
    "category": "Men's Shoes > Running",
    "color": "White/Black"
  }
}
```

---

### Step 4 - Confirmation visuelle

**Thought**: "J'ai des infos du site officiel. Je vais confirmer la couleur avec l'image produit."

**Tool call**: `analyze_image("https://cdn.shop.com/nike-shoe.jpg", ["Quelle est la couleur principale?", "Est-ce une chaussure de running?"])`

**Result**:
```json
{
  "observations": [
    { "attribute": "color", "value": "white with black accents", "confidence": 0.94, "reasoning": "Chaussure majoritairement blanche avec détails noirs visibles" },
    { "attribute": "shoe_type", "value": "sneaker/athletic shoe", "confidence": 0.91, "reasoning": "Design sportif avec semelle épaisse typique running" }
  ],
  "warnings": []
}
```

---

### Step 5 - Ajout des attributs sourcés

**Thought**: "J'ai maintenant des faits sourcés. Je vais ajouter les attributs manquants."

**Tool calls** (batch):

```typescript
add_attribute({
  attribute: "color",
  value: "White/Black",
  source: { type: "web", reference: "https://nike.com/air-max-90", evidence: "Color: White/Black" }
})

add_attribute({
  attribute: "gender",
  value: "male",
  source: { type: "web", reference: "https://nike.com/air-max-90", evidence: "Men's Shoes" }
})

add_attribute({
  attribute: "material",
  value: "Leather and textile",
  source: { type: "web", reference: "https://nike.com/air-max-90", evidence: "Leather and textile upper" }
})

add_attribute({
  attribute: "product_type",
  value: "Running Shoes",
  source: { type: "web", reference: "https://nike.com/air-max-90", evidence: "category: Men's Shoes > Running" }
})
```

---

### Step 6 - Optimisation du titre

**Thought**: "J'ai assez de faits sourcés pour améliorer le titre."

**Tool call**: 
```typescript
optimize_field({
  field: "title",
  current_value: "basket nike",
  context: {
    brand: "Nike",
    attributes: { color: "White/Black", gender: "male", product_type: "Running Shoes" },
    gathered_facts: [
      { fact: "Modèle: Air Max 90", source: "https://nike.com/air-max-90" },
      { fact: "Catégorie: Running", source: "https://nike.com/air-max-90" }
    ]
  },
  constraints: { max_length: 150, must_include: ["Nike", "Air Max 90"] }
})
```

**Result**:
```json
{
  "proposed_value": "Nike Air Max 90 - Chaussures de running homme blanc/noir",
  "changes_made": [
    "Ajout du nom de modèle (Air Max 90)",
    "Précision du type de produit (chaussures de running)",
    "Ajout du genre (homme)",
    "Ajout de la couleur (blanc/noir)"
  ],
  "facts_used": [
    { "fact": "Air Max 90", "source": "https://nike.com/air-max-90" },
    { "fact": "Men's Shoes", "source": "https://nike.com/air-max-90" },
    { "fact": "White/Black", "source": "https://nike.com/air-max-90" }
  ],
  "confidence": 0.92
}
```

---

### Step 7 - Validation avant commit

**Thought**: "Je dois valider que ma proposition est safe avant de l'appliquer."

**Tool call**:
```typescript
validate_proposal({
  field: "title",
  before: "basket nike",
  after: "Nike Air Max 90 - Chaussures de running homme blanc/noir",
  sources: [
    { type: "web", reference: "https://nike.com/air-max-90", evidence: "Nike Air Max 90 Men's Shoes... Color: White/Black" }
  ]
})
```

**Result**:
```json
{
  "valid": true,
  "risk_level": "low",
  "issues": [],
  "requires_human_review": false
}
```

---

### Step 8 - Commit

**Thought**: "La proposition est validée avec un risque faible. Je peux commit."

**Tool call**:
```typescript
commit_changes({
  product_id: "SKU789",
  changes: [
    {
      field: "title",
      before: "basket nike",
      after: "Nike Air Max 90 - Chaussures de running homme blanc/noir",
      sources: [{ type: "web", reference: "https://nike.com/air-max-90", evidence: "..." }],
      confidence: 0.92,
      validated: true
    },
    {
      field: "color",
      before: null,
      after: "White/Black",
      sources: [{ type: "web", reference: "https://nike.com/air-max-90", evidence: "..." }],
      confidence: 0.94,
      validated: true
    }
    // ... autres attributs
  ]
})
```

---

### Step 9 - Vérification finale

**Thought**: "Je re-analyse pour confirmer que le produit est maintenant compliant."

**Tool call**: `analyze_product("SKU789")`

**Result**:
```json
{
  "gmc_compliance": { "valid": true, "errors": [] },
  "quality_scores": {
    "title_quality": 0.88,
    "description_quality": 0.82,
    "completeness": 0.85,
    "agent_readiness": 0.86
  }
}
```

**Thought**: "Le produit est maintenant GMC-compliant et agent-ready. Mission accomplie."

**Status**: `completed`

---

## Résumé de la trace

| Step | Tool | Reasoning |
|------|------|-----------|
| 1 | `analyze_product` | Comprendre l'état initial |
| 2 | `web_search` | Chercher infos manquantes |
| 3 | `fetch_page` | Extraire détails de la source |
| 4 | `analyze_image` | Confirmer visuellement |
| 5 | `add_attribute` x4 | Ajouter faits sourcés |
| 6 | `optimize_field` | Améliorer le titre |
| 7 | `validate_proposal` | Vérifier avant commit |
| 8 | `commit_changes` | Appliquer les modifications |
| 9 | `analyze_product` | Vérification finale |

**Tokens utilisés**: ~4200
**Durée**: ~8s
**Sources citées**: 2 (web + vision)
