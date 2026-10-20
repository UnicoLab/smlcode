---
name: hve-data-science
description: Exploratory Data Analysis (EDA), Bronze/Silver/Gold DataOps tiering, DS_CATALOG_V1 conventions, and MVE experiment design.
triggers: data-science, eda, bronze-silver-gold, dataops, experiment-design, hypothesis, ablation, evaluation-metrics, mlops
agents: data-science-worker, experiment-designer-planner, python-worker, planner
user-invocable: true
---

# HVE Data Science, DataOps & Experiment Design

## 1. Minimum Viable Experiment (`experiment-designer-planner`)
- State a falsifiable hypothesis, baseline benchmark, primary success metric, and guard-rail metrics (latency, memory, regression ceiling).
- Define dataset splits, deterministic random seeds, and ablation variables before running experiments.

## 2. DataOps Tiering & `DS_CATALOG_V1` (`data-science-worker`)
- **Bronze (Raw)**: Immutable ingested payloads with schema/timestamp provenance; never mutate raw inputs in place.
- **Silver (Cleaned / Validated)**: Deduplicated, schema-validated, null-handled records with explicit data-quality assertions.
- **Gold (Feature / Evaluation Aggregates)**: Deterministic feature tables, evaluation summaries, and reproducible metric artifacts.
- Catalog datasets and transformations with clear input/output schemas, row-count invariants, and lineage notes.
