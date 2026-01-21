# UX Screens MVP

## 1. Dashboard

**Contenu** :
- Datasets récents avec status
- Jobs en cours avec progress
- Stats globales (produits enrichis, score moyen, proposals pending)
- CTA "Importer un flux"

---

## 2. Import

**Contenu** :
- Drag & drop TSV/CSV
- Preview des colonnes détectées
- Mapping GMC automatique (éditable)
- Bouton "Importer"

---

## 3. Dataset View

**Contenu** :
- Header : nom, stats (produits, score moyen, proposals pending)
- **Actions** : "Enrichir tout" / "Exporter"
- **Liste produits** (table) :
  - ID, titre, score agent-readiness, status, nb proposals
  - Filtres : status, score range
  - Click → Product detail

---

## 4. Product Detail (CLEF)

**Layout 2 colonnes** :

**Colonne gauche** :
- Image produit
- Données actuelles (card)
- Scores actuels (gauges)

**Colonne droite** :
- **Bouton "Enrichir ce produit"** → lance l'agent
- **Trace de l'agent** (si session active/terminée) :
  - Timeline des steps
  - Pour chaque step : thought + tool + result (collapsible)
- **Proposals** :
  - Liste des modifications proposées
  - Pour chaque : before/after diff, sources, confidence
  - Actions : Accept / Reject / Edit

---

## 5. Agent Live View

**Quand l'agent travaille** :
- Streaming du raisonnement en temps réel
- "Je cherche des informations sur le site officiel..."
- "J'ai trouvé le modèle exact : Air Max 90"
- "Je confirme la couleur avec l'image..."
- Progress indicator
- Bouton "Pause" / "Stop"

---

## 6. Proposals Review (Bulk)

**Pour reviewer plusieurs proposals** :
- Filtres : dataset, risk level, field, status
- Table :
  - Produit, champ, before → after (diff), confidence, risk
  - Checkbox multi-select
- Actions bulk : "Accept selected", "Reject selected"
- Vue "Review mode" : une proposal à la fois, navigation prev/next

---

## 7. Rules

**Contenu** :
- Liste des règles (hard rules)
- Pour chaque : nom, champ, condition, sévérité, créée par (system/agent/user)
- CRUD
- Les règles créées par l'agent sont marquées "🤖 Agent"

---

## 8. Export

**Contenu** :
- Choix format : TSV (GMC), CSV, JSON
- Options :
  - Inclure seulement produits enrichis
  - Inclure metadata (sources)
- Preview
- Download

---

## Navigation

```
┌─────────────────────────────────────┐
│  📊 Dashboard                       │
│  📁 Datasets                        │
│  📋 Proposals (badge: 42 pending)   │
│  📏 Rules                           │
└─────────────────────────────────────┘
```

---

## Wireframe Product Detail

```
┌─────────────────────────────────────────────────────────────┐
│ ← Back to dataset                          [Enrichir 🤖]   │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────┐    ┌────────────────────────────────────┐│
│  │              │    │ AGENT TRACE                        ││
│  │   [Image]    │    │                                    ││
│  │              │    │ ● Step 1: Analyzed product         ││
│  └──────────────┘    │   "Title too short, missing attrs" ││
│                      │                                    ││
│  ┌──────────────┐    │ ● Step 2: Web search               ││
│  │ Current Data │    │   Found: Nike Air Max 90           ││
│  │              │    │                                    ││
│  │ title: ...   │    │ ● Step 3: Vision analysis          ││
│  │ brand: Nike  │    │   Confirmed: white/black           ││
│  │ color: -     │    │                                    ││
│  └──────────────┘    └────────────────────────────────────┘│
│                                                             │
│  ┌──────────────┐    ┌────────────────────────────────────┐│
│  │ Scores       │    │ PROPOSALS                          ││
│  │              │    │                                    ││
│  │ Agent-ready: │    │ ┌────────────────────────────────┐ ││
│  │ [====  ] 42% │    │ │ title                          │ ││
│  │              │    │ │ - basket nike                  │ ││
│  │ Completeness:│    │ │ + Nike Air Max 90 - Running... │ ││
│  │ [===   ] 30% │    │ │                                │ ││
│  └──────────────┘    │ │ 📎 nike.com | Conf: 92%        │ ││
│                      │ │ [Accept] [Reject] [Edit]       │ ││
│                      │ └────────────────────────────────┘ ││
│                      └────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```
