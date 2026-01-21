# FeedEnrich

SaaS d'enrichissement de données produit orienté "commerce agentique".

## Architecture

Un **agent IA autonome** qui analyse et enrichit les produits en utilisant des outils :

- `analyze_product` - Analyser l'état et scorer la qualité
- `web_search` - Chercher des informations sur le web
- `fetch_page` - Extraire le contenu d'une page
- `analyze_image` - Observer les attributs visuels
- `optimize_field` - Améliorer titres et descriptions
- `add_attribute` - Ajouter des attributs sourcés
- `validate_proposal` - Vérifier les modifications
- `commit_changes` - Appliquer les changements

## Principe "No Invention"

L'agent ne peut **jamais** inventer de caractéristique produit. Chaque fait ajouté doit être :
- Présent dans le flux source
- Confirmé par une source web fiable
- Observable sur l'image produit

## Quick Start

### Prérequis

- Go 1.23+
- PostgreSQL 15+
- Clé API OpenAI

### Installation

```bash
# Clone
git clone https://github.com/benjamincozon/feedenrich.git
cd feedenrich

# Installer les dépendances
go mod tidy

# Configurer
cp env.example .env
# Éditer .env avec vos credentials

# Migrations DB
goose -dir migrations postgres "$DATABASE_URL" up

# Lancer
go run ./cmd/api
```

### Variables d'environnement

| Variable | Description | Requis |
|----------|-------------|--------|
| `DATABASE_URL` | URL PostgreSQL | Oui |
| `OPENAI_API_KEY` | Clé API OpenAI | Oui |
| `PORT` | Port du serveur (défaut: 8080) | Non |
| `SERPER_API_KEY` | Clé API Serper pour web search | Non |

## API

### Datasets

```
POST   /api/datasets/upload    Upload TSV/CSV
GET    /api/datasets           Liste des datasets
GET    /api/datasets/:id       Détails d'un dataset
DELETE /api/datasets/:id       Supprimer
GET    /api/datasets/:id/export Export enrichi
```

### Agent

```
POST   /api/products/:id/enrich      Enrichir un produit
POST   /api/datasets/:id/enrich      Enrichir tout le dataset
GET    /api/agent/sessions/:id       Status de la session
GET    /api/agent/sessions/:id/trace Trace complète du raisonnement
```

### Proposals

```
GET    /api/proposals           Liste des propositions
PATCH  /api/proposals/:id       Accept/Reject/Edit
POST   /api/proposals/bulk      Actions en masse
```

## Deploy sur Railway

1. Créer un nouveau projet Railway
2. Ajouter un service PostgreSQL
3. Connecter le repo GitHub
4. Ajouter les variables d'environnement
5. Deploy !

## Docs

Voir `/docs` pour :
- Architecture détaillée
- Spécification des tools
- Modèle de données
- Exemples de traces agent
