# Policies : No-Invention & Human Gate

## 1. Politique "No Invention" (CRITIQUE)

### Principe fondamental

> **L'IA ne doit JAMAIS inventer ou inférer une caractéristique produit qui n'est pas :**
> 1. Présente dans le flux source (TSV)
> 2. Confirmée par une source web fiable (avec citation)
> 3. Observable sans ambiguïté sur l'image produit

### Règles concrètes

| Catégorie | Autorisé | Interdit |
|-----------|----------|----------|
| **Reformulation** | ✅ Réécrire titre pour clarté | ❌ Ajouter "imperméable" si non sourcé |
| **Structuration** | ✅ Extraire attributs du texte existant | ❌ Deviner la taille |
| **Complétion** | ✅ Ajouter couleur vue sur image | ❌ Supposer le matériau |
| **Amélioration SEO** | ✅ Ajouter synonymes vérifiés | ❌ Inventer des bénéfices |

### Implémentation technique

```typescript
interface ProposalValidation {
  // Chaque fait ajouté doit avoir une source
  addedFacts: {
    fact: string;
    source: FactSource;
  }[];
  
  // Faits sans source = REJET automatique
  unsourcedFacts: string[]; // Doit être vide
}

type FactSource = 
  | { type: "feed"; field: string; value: string }
  | { type: "web"; url: string; snippet: string; fetchedAt: Date }
  | { type: "vision"; observation: string; confidence: number; imageUrl: string };

// Le Controller agent applique cette vérification
function validateNoInvention(proposal: Proposal): ControlResult {
  const addedContent = diff(proposal.before, proposal.after);
  
  for (const fact of extractFacts(addedContent)) {
    const source = findSource(fact, proposal.sources);
    if (!source) {
      return {
        approved: false,
        reason: `Fait non sourcé: "${fact}"`,
        risk: "high"
      };
    }
  }
  
  return { approved: true, risk: "low" };
}
```

### Exemples

**✅ VALIDE**
```json
{
  "field": "title",
  "before": "Chaussure running",
  "after": "Chaussure de running homme Nike Air Zoom",
  "sources": [
    { "type": "feed", "field": "brand", "value": "Nike" },
    { "type": "feed", "field": "mpn", "value": "Air Zoom" },
    { "type": "feed", "field": "gender", "value": "male" }
  ]
}
```

**❌ REJETÉ**
```json
{
  "field": "title",
  "before": "Chaussure running",
  "after": "Chaussure de running imperméable ultra-légère",
  "sources": [],
  "rejection_reason": "Faits non sourcés: 'imperméable', 'ultra-légère'"
}
```

---

## 2. Politique "Human Gate"

### Quand forcer la validation humaine

| Trigger | Risk Level | Action |
|---------|------------|--------|
| Aucune source pour un fait ajouté | 🔴 High | Block + Human required |
| Source web mais confiance < 0.7 | 🟠 Medium | Flag for review |
| Changement de claim sensible | 🔴 High | Human required |
| Vision seul sur attribut technique | 🟠 Medium | Flag for review |
| Tout low risk + bien sourcé | 🟢 Low | Auto-accept (si config) |

### Claims sensibles (toujours human gate)

- Certifications (bio, CE, norme)
- Allégations santé / performance
- Garanties
- Compatibilités techniques
- Ingrédients / composition
- Pays d'origine
- Prix / promotions

### Configuration par dataset

```json
{
  "human_gate_config": {
    "auto_accept_low_risk": true,
    "sensitive_fields": ["certification", "warranty", "ingredients"],
    "confidence_threshold": 0.75,
    "require_source_for_fields": ["material", "dimensions", "weight"],
    "max_auto_accept_per_batch": 100
  }
}
```

### Workflow de review

```
Proposal générée
       │
       ▼
┌──────────────┐
│ Risk = low?  │──yes──▶ Auto-accept (si config)
└──────┬───────┘
       │ no
       ▼
┌──────────────┐
│ Risk = high? │──yes──▶ Block until human review
└──────┬───────┘
       │ no (medium)
       ▼
┌──────────────────────┐
│ Flag for review      │
│ (peut être auto      │
│  après X jours)      │
└──────────────────────┘
```

### UI de review

Pour chaque proposal à risque :

1. **Contexte visible** :
   - Produit complet (image, données)
   - Before/After avec diff highlighting
   - Sources citées (cliquables)
   - Confidence score + explication

2. **Actions** :
   - ✅ **Accept** : applique le changement
   - ❌ **Reject** : garde l'original
   - ✏️ **Edit** : modifier la proposition
   - 🔍 **Request more sources** : relancer retrieval

3. **Bulk actions** :
   - Accept all with source confidence > 0.9
   - Reject all without sources
   - Accept all in cluster X

---

## 3. Audit Trail

Chaque décision est loggée :

```json
{
  "proposal_id": "uuid",
  "product_id": "uuid",
  "decision": "accepted",
  "decided_by": "human:user@email.com",  // ou "auto:low_risk_policy"
  "decided_at": "2024-01-15T11:30:00Z",
  "rationale": "Source vérifiée sur site officiel",
  "confidence_at_decision": 0.87,
  "risk_at_decision": "low"
}
```
