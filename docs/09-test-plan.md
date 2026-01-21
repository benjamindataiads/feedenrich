# Plan de Test

## 1. Métriques clés à suivre

| Métrique | Description | Target MVP |
|----------|-------------|------------|
| **Agent Readiness Score** | Score moyen post-enrichissement | > 0.75 |
| **Taux de violation no-invention** | Proposals avec faits non sourcés | < 5% |
| **Taux de validation humaine** | Proposals nécessitant review | < 30% |
| **Taux d'acceptation** | Proposals acceptées / total | > 80% |
| **Temps de traitement** | Temps moyen par produit | < 10s |
| **Coût tokens/produit** | Tokens OpenAI par produit | < 5000 |
| **Hard rules compliance** | Produits sans erreur hard après enrichissement | > 95% |

---

## 2. Jeux de test

### Dataset minimal (10 produits)

```json
{
  "name": "test_minimal",
  "products": [
    { "id": "GOOD_001", "title": "Nike Air Max 90 Running Shoes Men", "description": "Complete description...", "brand": "Nike" },
    { "id": "BAD_002", "title": "chaussure", "description": "", "brand": "" },
    { "id": "PARTIAL_003", "title": "veste outdoor", "description": "bonne veste", "brand": "North Face" }
  ]
}
```

### Dataset stress (1000+ produits)

- Mix de catégories (shoes, clothing, electronics)
- 30% données complètes
- 50% données partielles
- 20% données très incomplètes

### Dataset edge cases

| ID | Cas | Attendu |
|----|-----|---------|
| EDGE_001 | Titre avec caractères spéciaux | Normalisation correcte |
| EDGE_002 | Description > 5000 chars | Troncature intelligente |
| EDGE_003 | Image 404 | Skip vision, fallback graceful |
| EDGE_004 | Produit avec claims santé | Human gate forcé |
| EDGE_005 | GTIN invalide | Hard error détecté |
| EDGE_006 | Prix format bizarre "89,99€" | Parsing correct |

---

## 3. Tests unitaires par composant

### Ingestion
- [ ] Parse TSV avec différents delimiters
- [ ] Détection encoding (UTF-8, Latin-1)
- [ ] Mapping colonnes GMC
- [ ] Gestion lignes malformées

### Hard Rules Engine
- [ ] Validation longueur title (30-150 chars)
- [ ] Validation format GTIN
- [ ] Validation URL image
- [ ] Détection mots interdits

### Smart Auditor
- [ ] Score clarity corrélé à lisibilité réelle
- [ ] Score completeness vs champs GMC requis
- [ ] Findings actionnables

### Retriever
- [ ] Web search retourne résultats pertinents
- [ ] Extraction facts avec citations
- [ ] Timeout / retry sur fetch échoué
- [ ] Rate limiting respecté

### Vision
- [ ] Détection couleur fiable
- [ ] Confiance basse sur images ambiguës
- [ ] Skip sur image non chargeable

### Optimizer
- [ ] Proposals respectent contraintes hard
- [ ] Sources présentes pour chaque fait ajouté
- [ ] Confidence score cohérent

### Controller
- [ ] Détection facts non sourcés
- [ ] Risk assessment correct
- [ ] No false positives (reject valide)

---

## 4. Tests d'intégration pipeline

### Scénario nominal
```
1. Upload TSV (100 produits)
2. Job démarre → tous les steps passent
3. Proposals générées (avg 2 par produit)
4. Review (accept all low risk)
5. Export final
6. Vérifier: 0 hard errors, agent_readiness > 0.75
```

### Scénario échec/reprise
```
1. Upload TSV
2. Simuler échec au step ENRICHING (API timeout)
3. Job → FAILED
4. Retry job
5. Reprise au step ENRICHING (pas recommencer depuis début)
6. Completion normale
```

### Scénario no-invention
```
1. Upload TSV avec produit minimal
2. Forcer optimizer à "inventer" (prompt adversarial test)
3. Controller doit REJECT
4. Human gate triggered
5. Vérifier audit trail
```

---

## 5. Tests de non-régression

Après chaque changement de prompt ou règle :

1. Rejouer dataset de référence (100 produits annotés)
2. Comparer scores vs baseline
3. Alerter si dégradation > 5%

---

## 6. Tests de performance

| Test | Méthode | Seuil |
|------|---------|-------|
| Latency import | Upload 10MB TSV | < 5s |
| Throughput pipeline | 1000 produits batch | < 30 min |
| Concurrent jobs | 5 jobs parallèles | Pas de deadlock |
| Memory usage | Worker sous charge | < 1GB RAM |

---

## 7. Tests sécurité

- [ ] Upload fichier malicieux (script injection)
- [ ] SQL injection via search
- [ ] Rate limiting API
- [ ] Validation permissions (auth)

---

## 8. Checklist go-live

- [ ] Dataset réel client (1000+ produits) traité sans erreur
- [ ] Taux validation humaine acceptable (< 30%)
- [ ] Temps traitement acceptable
- [ ] Coût tokens dans budget
- [ ] Aucune violation no-invention non détectée
- [ ] Export compatible GMC (validation Google)
- [ ] Audit trail complet et requêtable
