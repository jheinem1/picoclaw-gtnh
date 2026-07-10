# DH Cache Chunk Converter

Standalone converter for Minecraft 1.7.10 Anvil region chunks to the Distant
Horizons sqlite cache format used by the local GTNH instance.

This tool is intentionally local-only. It reads an existing save's `level.dat`
and `region/*.mca`, writes a new `DistantHorizons.sqlite`, and can stage that
sqlite into a new singleplayer validation save.

Build:

```sh
tools/dh-cache-converter/build.sh
```

Run:

```sh
CP="$(cat tools/dh-cache-converter/build/classpath.txt)"
java -Xmx4g -cp "$CP" DhCacheChunkConverter \
  --save "/path/to/save" \
  --out "/tmp/DistantHorizons.sqlite" \
  --radius-chunks 32 \
  --compression Z_STD_BLOCK \
  --overwrite \
  --validation-world "/path/to/.minecraft/saves/DH_Static_Validation"
```

The default center is the world spawn from `level.dat`. Use
`--center-chunk X,Z` to override it.

The converter builds DH parent LOD rows by default. To add or rebuild parent
LODs in an existing sqlite cache without rereading Anvil chunks:

```sh
CP="$(tr -d '\n' < tools/dh-cache-converter/build/classpath.txt)"
java -Xmx6g -cp "$CP" DhCacheChunkConverter \
  --build-parent-lods-only "/path/to/DistantHorizons.sqlite" \
  --compression Z_STD_BLOCK
```

Validation helpers:

```sh
java -Xmx2g -cp "$CP" DhCacheChunkConverter \
  --validate-db "/path/to/DistantHorizons.sqlite"

java -Xmx4g -cp "$CP" DhCacheChunkConverter \
  --scan-db-mappings "/path/to/DistantHorizons.sqlite"

java -Xmx2g -cp "$CP" DhCacheChunkConverter \
  --inspect-db-pos "/path/to/DistantHorizons.sqlite" 0,0,-10

java -Xmx4g -cp "$CP" DhCacheChunkConverter \
  --save "/path/to/save" \
  --rebuild-chunk-hashes-only "/path/to/DistantHorizons.sqlite" \
  --radius-chunks 512
```

The converter sanitizes Forge registry names discovered from `level.dat` before
serializing them into DH mappings. This strips control bytes seen in GTNH
`Blocks16` saves so DH does not receive invalid block strings such as prefixed
mod IDs. Biomes are loaded from vanilla IDs and Biomes O' Plenty `ids.cfg`;
unknown biome IDs are serialized as `Unknown_<id>` instead of the special
`EMPTY` biome. Chunk hashes are generated with DH's sampled block/height-map
hash using `EMPTY` biomes so the DH client does not overwrite imported clean
server-cache rows with live multiplayer chunk wrappers that serialize empty
biomes. Exposed non-air segments use the light from the air directly above
their top boundary so high-detail parent LOD top faces do not render black when
the source Anvil block's own skylight nibble is zero.
