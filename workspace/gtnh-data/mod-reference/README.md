# Mod reference corpus

This directory contains bounded, read-only reference records generated from exact installed
GTNH mod artifacts. GregGPT searches the TSV files with `mod_reference_search`; it does not
receive arbitrary filesystem access.

Each row records the mod ID, version, artifact name and SHA-256, source symbol, subject, and
artifact-derived content. Generate the Binnie Botany flower-color mutation index with:

```sh
scripts/build_binnie_botany_reference.sh /path/to/binnie-mods-VERSION.jar \
  workspace/gtnh-data/mod-reference/binnie-botany-VERSION.tsv
```

Generate the Thaumic Energistics automation reference from its exact installed artifact with:

```sh
scripts/build_thaumic_energistics_reference.sh /path/to/thaumicenergistics-VERSION.jar \
  workspace/gtnh-data/mod-reference/thaumic-energistics-VERSION.tsv
```

A missing mod means the local corpus has no evidence for it; it is not evidence that the
mechanic does not exist.
