# Agent Tools

## Toolbox de l'agent

L'agent a accès à ces outils et décide quand/comment les utiliser.

---

## 1. `analyze_product`

**But** : Comprendre l'état actuel du produit, identifier les gaps et scorer la qualité.

```typescript
analyze_product(product_id: string): {
  current_data: ProductData,
  gmc_compliance: {
    valid: boolean,
    errors: { field: string, issue: string, severity: "error" | "warning" }[]
  },
  quality_scores: {
    title_quality: number,      // 0-1
    description_quality: number,
    completeness: number,
    agent_readiness: number
  },
  missing_attributes: string[],  // ["color", "material", "size"]
  improvement_opportunities: {
    field: string,
    current_issue: string,
    potential_action: string
  }[]
}
```

**Quand l'agent l'utilise** : En début de session pour comprendre le produit, ou après des modifications pour re-évaluer.

---

## 2. `web_search`

**But** : Chercher des informations factuelles sur le web.

```typescript
web_search(query: string, options?: {
  site?: string,        // ex: "nike.com" pour limiter au site officiel
  num_results?: number
}): {
  results: {
    title: string,
    url: string,
    snippet: string
  }[]
}
```

**Quand l'agent l'utilise** : Pour sourcer des caractéristiques manquantes (matière, dimensions, specs).

---

## 3. `fetch_page`

**But** : Récupérer et parser le contenu d'une page web.

```typescript
fetch_page(url: string): {
  title: string,
  content: string,       // texte nettoyé
  structured_data?: any, // JSON-LD si présent
  fetched_at: string
}
```

**Quand l'agent l'utilise** : Après un `web_search` pour extraire des infos détaillées d'une page pertinente.

---

## 4. `analyze_image`

**But** : Observer les caractéristiques visuelles du produit.

```typescript
analyze_image(image_url: string, questions?: string[]): {
  observations: {
    attribute: string,    // "color", "style", "has_logo"
    value: string,
    confidence: number,   // 0-1
    reasoning: string
  }[],
  warnings: string[]      // "Image floue", "Plusieurs produits visibles"
}
```

**Questions exemples** : `["Quelle est la couleur principale?", "Y a-t-il une capuche?", "Type de col?"]`

**Quand l'agent l'utilise** : Pour confirmer visuellement des attributs (couleur, forme, style) - jamais pour inventer des specs techniques.

---

## 5. `optimize_field`

**But** : Générer une version améliorée d'un champ texte.

```typescript
optimize_field(params: {
  field: "title" | "description",
  current_value: string,
  context: {
    brand?: string,
    category?: string,
    attributes?: Record<string, string>,
    gathered_facts?: { fact: string, source: string }[]
  },
  constraints: {
    max_length?: number,
    must_include?: string[],
    must_exclude?: string[],
    tone?: "factual" | "marketing"
  }
}): {
  proposed_value: string,
  changes_made: string[],
  facts_used: { fact: string, source: string }[],
  confidence: number
}
```

**Quand l'agent l'utilise** : Quand il a identifié qu'un champ est de mauvaise qualité ET qu'il a assez de faits sourcés pour l'améliorer.

---

## 6. `add_attribute`

**But** : Ajouter un attribut manquant avec source obligatoire.

```typescript
add_attribute(params: {
  attribute: string,     // "color", "material", "gender"
  value: string,
  source: {
    type: "feed" | "web" | "vision",
    reference: string,   // URL ou champ source
    evidence: string     // snippet ou observation
  }
}): {
  success: boolean,
  attribute: string,
  value: string,
  source_recorded: boolean
}
```

**Quand l'agent l'utilise** : Après avoir trouvé un fait via `web_search`, `fetch_page` ou `analyze_image`.

---

## 7. `create_rule`

**But** : Créer une règle de validation (hard rule) pour le dataset.

```typescript
create_rule(params: {
  name: string,
  type: "hard",
  scope: "field" | "product",
  field?: string,
  condition: {
    operator: "max_length" | "min_length" | "required" | "pattern" | "forbidden_words" | "custom",
    value: any
  },
  message: string,
  severity: "error" | "warning"
}): {
  rule_id: string,
  applied_to: number  // nombre de produits affectés
}
```

**Quand l'agent l'utilise** : Quand il détecte un pattern de problème récurrent dans le dataset (ex: tous les titres trop courts).

---

## 8. `validate_proposal`

**But** : Vérifier qu'une proposition respecte les contraintes (no-invention, sources, rules).

```typescript
validate_proposal(params: {
  field: string,
  before: string,
  after: string,
  sources: { type: string, reference: string, evidence: string }[]
}): {
  valid: boolean,
  risk_level: "low" | "medium" | "high",
  issues: {
    type: "unsourced_fact" | "rule_violation" | "invention_detected",
    detail: string
  }[],
  requires_human_review: boolean
}
```

**Quand l'agent l'utilise** : Avant de commit une modification, pour s'assurer qu'elle est safe.

---

## 9. `commit_changes`

**But** : Appliquer les modifications validées au produit.

```typescript
commit_changes(params: {
  product_id: string,
  changes: {
    field: string,
    before: string,
    after: string,
    sources: { type: string, reference: string, evidence: string }[],
    confidence: number,
    validated: boolean
  }[]
}): {
  success: boolean,
  product_version: number,
  changes_applied: number,
  pending_human_review: number
}
```

**Quand l'agent l'utilise** : À la fin de son travail sur un produit, quand il a des propositions validées.

---

## 10. `request_human_review`

**But** : Escalader une décision à un humain.

```typescript
request_human_review(params: {
  product_id: string,
  question: string,
  context: any,
  options?: string[]  // choix possibles pour l'humain
}): {
  review_id: string,
  status: "pending"
}
```

**Quand l'agent l'utilise** : Quand il n'est pas sûr (confiance < seuil) ou quand le risque est élevé.

---

## Contraintes système (injectées dans le prompt)

```
CONTRAINTES ABSOLUES:
1. Tu ne dois JAMAIS inventer une caractéristique produit
2. Chaque fait ajouté DOIT avoir une source (feed, web, ou vision)
3. Si tu n'es pas sûr → request_human_review
4. Toujours validate_proposal avant commit_changes
5. Pour les attributs techniques (matière, dimensions, poids) → source web obligatoire
6. Pour les attributs visuels (couleur, style) → vision acceptable si confidence > 0.85
```
