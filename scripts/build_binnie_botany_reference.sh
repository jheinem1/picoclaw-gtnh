#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 /path/to/binnie-mods-VERSION.jar /path/to/output.tsv" >&2
  exit 2
fi

jar_path=$1
output_path=$2
artifact=$(basename "$jar_path")
version=${artifact#binnie-mods-}
version=${version%.jar}
artifact_sha256=$(sha256sum "$jar_path" | awk '{print $1}')
output_dir=$(dirname "$output_path")
mkdir -p "$output_dir"
tmp_output=$(mktemp "$output_dir/.binnie-botany-reference.XXXXXX")
trap 'rm -f "$tmp_output"' EXIT

printf 'mod_id\tversion\tartifact\tartifact_sha256\tsource\tsubject\tcontent\n' > "$tmp_output"
javap -classpath "$jar_path" -p -c binnie.botany.genetics.EnumFlowerColor |
  awk -v version="$version" -v artifact="$artifact" -v artifact_sha256="$artifact_sha256" '
    /public static void setupMutations/ { inside=1; next }
    inside && /getstatic/ && /Field/ {
      field=$0
      sub(/^.*Field /, "", field)
      sub(/:.*/, "", field)
      first=second
      second=third
      third=field
    }
    inside && /(bipush|sipush)/ { chance=$NF }
    inside && /Method addMix/ {
      method=first " + " second " -> " third " (" chance "%)"
      if (methods[third] == "") methods[third]=method
      else methods[third]=methods[third] "; " method
    }
    inside && /^  public static binnie.botany.genetics.EnumFlowerColor get\(/ { inside=0 }
    END {
      for (result in methods) {
        print "binnie-botany\t" version "\t" artifact "\t" artifact_sha256 "\tbinnie.botany.genetics.EnumFlowerColor.setupMutations\t" result "\t" methods[result]
      }
    }
  ' | LC_ALL=C sort -t $'\t' -k6,6 >> "$tmp_output"

chmod 0644 "$tmp_output"
mv "$tmp_output" "$output_path"
trap - EXIT
printf 'wrote %s from %s (%s)\n' "$output_path" "$artifact" "$artifact_sha256"
