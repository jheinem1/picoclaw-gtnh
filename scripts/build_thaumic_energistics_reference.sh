#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 /path/to/thaumicenergistics-VERSION.jar /path/to/output.tsv" >&2
  exit 2
fi

jar_path=$1
output_path=$2
artifact=$(basename "$jar_path")

for command in awk javap mktemp sha256sum unzip; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "required command not found: $command" >&2
    exit 1
  fi
done

if [[ ! -f "$jar_path" ]]; then
  echo "artifact not found: $jar_path" >&2
  exit 1
fi

metadata=$(unzip -p "$jar_path" mcmod.info)
mod_id=$(awk -F '"' '/"modid"/ { print $4; exit }' <<<"$metadata")
version=$(awk -F '"' '/"version"/ { print $4; exit }' <<<"$metadata")
if [[ "$mod_id" != "thaumicenergistics" || -z "$version" ]]; then
  echo "artifact is not a recognized Thaumic Energistics jar: $jar_path" >&2
  exit 1
fi

artifact_sha256=$(sha256sum "$jar_path" | awk '{print $1}')
output_dir=$(dirname "$output_path")
mkdir -p "$output_dir"
tmp_dir=$(mktemp -d "$output_dir/.thaumic-energistics-reference.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT

lang_path="$tmp_dir/en_US.lang"
tmp_output="$tmp_dir/output.tsv"
unzip -p "$jar_path" assets/thaumicenergistics/lang/en_US.lang > "$lang_path"

printf 'mod_id\tversion\tartifact\tartifact_sha256\tsource\tsubject\tcontent\n' > "$tmp_output"

emit_record() {
  local source=$1
  local subject=$2
  local content=$3
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$mod_id" "$version" "$artifact" "$artifact_sha256" "$source" "$subject" "$content" >> "$tmp_output"
}

emit_lang_record() {
  local key=$1
  local subject=$2
  local content
  if ! content=$(awk -v key="$key" '
      index($0, key "=") == 1 {
        sub(/^[^=]*=/, "")
        print
        found=1
        exit
      }
      END { if (!found) exit 1 }
    ' "$lang_path"); then
    echo "required language key not found: $key" >&2
    exit 1
  fi
  content=$(printf '%s' "$content" |
    sed -E 's/§[0-9a-fklmnor]//gI; s/<BR>/ /g; s/<LINE>/ /g; s/[[:space:]]+/ /g')
  emit_record "assets/thaumicenergistics/lang/en_US.lang:$key" "$subject" "$content"
}

emit_lang_record \
  "thaumicenergistics.research_page.TEARCANETERM.1" \
  "Thaumcraft AE autocrafting - Arcane Crafting Terminal"
emit_lang_record \
  "thaumicenergistics.research_page.TEESSPROV.1" \
  "Thaumcraft AE essentia transport - Essentia Provider"
emit_lang_record \
  "thaumicenergistics.research_page.TEINFPROV.1" \
  "Thaumcraft AE infusion autocrafting - Infusion Provider"
emit_lang_record \
  "thaumicenergistics.research_page.TEADVINFPROV.1" \
  "Thaumcraft AE infusion autocrafting - Advanced Infusion Provider"
emit_lang_record \
  "thaumicenergistics.research_page.TEKNOWLEDGEINSCRIBER.1" \
  "Thaumcraft AE arcane autocrafting patterns - Knowledge Core and Knowledge Inscriber"
emit_lang_record \
  "thaumicenergistics.research_page.TEKNOWLEDGEINSCRIBER.2" \
  "Thaumcraft AE arcane autocrafting patterns - encoding and deleting Knowledge Core recipes"
emit_lang_record \
  "thaumicenergistics.research_page.TEARCANEASSEMBLER.1" \
  "Thaumcraft AE arcane autocrafting - Arcane Assembler construction"
emit_lang_record \
  "thaumicenergistics.research_page.TEARCANEASSEMBLER.2" \
  "Thaumcraft AE arcane autocrafting - Arcane Assembler pattern storage, vis, and acceleration"
emit_lang_record \
  "thaumicenergistics.research_page.TEARCANEASSEMBLER.3" \
  "Thaumcraft AE arcane autocrafting - Arcane Assembler vis discount and warp cost"
emit_lang_record \
  "thaumicenergistics.research_page.TEDISTILLATIONPATTERNENCODER.1" \
  "Thaumcraft AE essentia autocrafting patterns - Distillation Pattern Encoder"

arcane_pattern=$(javap -classpath "$jar_path" -p -constants \
  thaumicenergistics.common.integration.tc.ArcaneCraftingPattern)
if [[ "$arcane_pattern" != *"implements appeng.api.networking.crafting.ICraftingPatternDetails"* ||
      "$arcane_pattern" != *"private static final int GRID_SIZE = 9;"* ||
      "$arcane_pattern" != *"public int getAspectCost(thaumcraft.api.aspects.Aspect);"* ]]; then
  echo "unexpected ArcaneCraftingPattern bytecode API in $artifact" >&2
  exit 1
fi
emit_record \
  "thaumicenergistics.common.integration.tc.ArcaneCraftingPattern" \
  "Thaumcraft AE arcane crafting pattern API" \
  "ArcaneCraftingPattern implements AE2 ICraftingPatternDetails, uses a 9-slot crafting grid, exposes AE inputs and outputs, and records the vis aspect cost."

arcane_assembler=$(javap -classpath "$jar_path" -p -constants \
  thaumicenergistics.common.tiles.TileArcaneAssembler)
if [[ "$arcane_assembler" != *"implements appeng.api.networking.crafting.ICraftingProvider"* ||
      "$arcane_assembler" != *"private static final int BASE_TICKS_PER_CRAFT = 20;"* ||
      "$arcane_assembler" != *"public static final int MAX_STORED_CVIS = 1500;"* ]]; then
  echo "unexpected TileArcaneAssembler bytecode API in $artifact" >&2
  exit 1
fi
emit_record \
  "thaumicenergistics.common.tiles.TileArcaneAssembler" \
  "Thaumcraft AE Arcane Assembler crafting provider API" \
  "TileArcaneAssembler implements AE2 ICraftingProvider, has a base craft time of 20 ticks, and can store up to 1500 centivis."

header=$(head -n 1 "$tmp_output")
tail -n +2 "$tmp_output" | LC_ALL=C sort -t $'\t' -k6,6 > "$tmp_dir/sorted.tsv"
printf '%s\n' "$header" > "$tmp_output"
cat "$tmp_dir/sorted.tsv" >> "$tmp_output"

chmod 0644 "$tmp_output"
mv "$tmp_output" "$output_path"
trap - EXIT
rm -rf "$tmp_dir"
printf 'wrote %s from %s (%s)\n' "$output_path" "$artifact" "$artifact_sha256"
