import com.seibel.distanthorizons.api.enums.config.EDhApiDataCompressionMode;
import com.seibel.distanthorizons.api.enums.config.EDhApiWorldCompressionMode;
import com.seibel.distanthorizons.api.enums.rendering.EDhApiBlockMaterial;
import com.seibel.distanthorizons.api.enums.worldGeneration.EDhApiLevelType;
import com.seibel.distanthorizons.api.enums.worldGeneration.EDhApiWorldGenerationStep;
import com.seibel.distanthorizons.api.interfaces.render.IDhApiCustomRenderRegister;
import com.seibel.distanthorizons.api.interfaces.world.IDhApiLevelWrapper;
import com.seibel.distanthorizons.core.dataObjects.fullData.sources.FullDataSourceV2;
import com.seibel.distanthorizons.core.dataObjects.transformers.FullDataOcclusionCuller;
import com.seibel.distanthorizons.core.dependencyInjection.SingletonInjector;
import com.seibel.distanthorizons.core.level.IDhLevel;
import com.seibel.distanthorizons.core.pos.DhChunkPos;
import com.seibel.distanthorizons.core.pos.DhSectionPos;
import com.seibel.distanthorizons.core.sql.dto.ChunkHashDTO;
import com.seibel.distanthorizons.core.sql.dto.FullDataSourceV2DTO;
import com.seibel.distanthorizons.core.sql.repo.AbstractDhRepo;
import com.seibel.distanthorizons.core.sql.repo.ChunkHashRepo;
import com.seibel.distanthorizons.core.sql.repo.FullDataSourceV2Repo;
import com.seibel.distanthorizons.core.util.FullDataPointUtil;
import com.seibel.distanthorizons.core.wrapperInterfaces.IWrapperFactory;
import com.seibel.distanthorizons.core.wrapperInterfaces.block.IBlockStateWrapper;
import com.seibel.distanthorizons.core.wrapperInterfaces.chunk.IChunkWrapper;
import com.seibel.distanthorizons.core.wrapperInterfaces.render.objects.IDhGenericObjectVertexBufferContainer;
import com.seibel.distanthorizons.core.wrapperInterfaces.render.objects.ILodContainerUniformBufferWrapper;
import com.seibel.distanthorizons.core.wrapperInterfaces.render.objects.IVertexBufferWrapper;
import com.seibel.distanthorizons.core.wrapperInterfaces.render.renderPass.IDhGenericRenderer;
import com.seibel.distanthorizons.core.wrapperInterfaces.world.IBiomeWrapper;
import com.seibel.distanthorizons.core.wrapperInterfaces.world.IDimensionTypeWrapper;
import com.seibel.distanthorizons.core.wrapperInterfaces.world.ILevelWrapper;
import com.seibel.distanthorizons.core.wrapperInterfaces.worldGeneration.IBatchGeneratorEnvironmentWrapper;
import it.unimi.dsi.fastutil.objects.ObjectOpenHashSet;
import it.unimi.dsi.fastutil.longs.LongArrayList;

import java.awt.Color;
import java.io.BufferedInputStream;
import java.io.ByteArrayInputStream;
import java.io.DataInputStream;
import java.io.EOFException;
import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.Statement;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Comparator;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.TreeMap;
import java.util.TreeSet;
import java.util.zip.GZIPInputStream;
import java.util.zip.InflaterInputStream;

public final class DhCacheChunkConverter {
    private static final int WORLD_MIN_Y = 0;
    private static final int WORLD_MAX_Y_EXCLUSIVE = 256;
    private static final int SECTION_BLOCK_WIDTH = 64;
    private static final int CHUNK_WIDTH = 16;
    private static final int CHUNKS_PER_REGION = 32;
    private static final int CHUNKS_PER_DH_SECTION = 4;
    private static final StaticBlockWrapper AIR_BLOCK = StaticBlockWrapper.of("AIR");
    private static final StaticBiomeWrapper EMPTY_BIOME = StaticBiomeWrapper.of("EMPTY");
    private static final StaticBiomeWrapper PLAINS_BIOME = StaticBiomeWrapper.of("biome:Plains");

    public static void main(String[] rawArgs) throws Exception {
        Args args = Args.parse(rawArgs);
        StaticWrapperFactory.register();

        if (args.validateDbOnly != null) {
            validateDatabase(args.validateDbOnly);
            return;
        }
        if (args.scanDbMappings != null) {
            scanDatabaseMappings(args.scanDbMappings);
            return;
        }
        if (args.inspectDbPos != null) {
            inspectDatabasePosition(args.inspectDbPos, args.inspectDetailLevel, args.inspectPosX, args.inspectPosZ);
            return;
        }
        if (args.inspectSaveChunk != null) {
            inspectSaveChunk(args.inspectSaveChunk, args.inspectChunkX, args.inspectChunkZ);
            return;
        }
        if (args.buildParentLodsOnly != null) {
            StaticWrapperFactory.register();
            buildParentLods(args.buildParentLodsOnly, args.compressionMode);
            validateDatabase(args.buildParentLodsOnly);
            return;
        }
        if (args.rebuildChunkHashesOnly != null) {
            rebuildChunkHashes(args);
            return;
        }

        if (args.save == null || args.out == null) {
            throw new IllegalArgumentException("Required: --save <world save> --out <DistantHorizons.sqlite>");
        }

        convert(args);
    }

    private static void convert(Args args) throws Exception {
        Path save = args.save.toAbsolutePath();
        Path regionDir = save.resolve("region");
        Path levelDat = save.resolve("level.dat");
        if (!Files.isDirectory(regionDir)) {
            throw new IllegalArgumentException("Missing region directory: " + regionDir);
        }
        if (!Files.isRegularFile(levelDat)) {
            throw new IllegalArgumentException("Missing level.dat: " + levelDat);
        }

        Nbt.Compound levelRoot = readLevelDat(levelDat);
        Nbt.Compound data = levelRoot.compound("Data");
        long seed = data.longValue("RandomSeed", 0);
        String generator = data.string("generatorName", data.string("GeneratorName", ""));
        int spawnX = data.intValue("SpawnX", 0);
        int spawnY = data.intValue("SpawnY", 0);
        int spawnZ = data.intValue("SpawnZ", 0);
        int centerChunkX = args.centerChunkSet ? args.centerChunkX : floorDiv(spawnX, CHUNK_WIDTH);
        int centerChunkZ = args.centerChunkSet ? args.centerChunkZ : floorDiv(spawnZ, CHUNK_WIDTH);

        Map<Integer, String> blockNames = vanillaBlockNames();
        DiscoveryStats discoveredIds = discoverForgeIds(levelRoot, blockNames);
        Map<Integer, String> biomeNames = biomeNames(save);

        System.out.printf(Locale.ROOT,
                "level seed=%d generator=%s spawn=(%d,%d,%d) centerChunk=(%d,%d)%n",
                seed, generator, spawnX, spawnY, spawnZ, centerChunkX, centerChunkZ);
        System.out.printf(Locale.ROOT,
                "id maps: blocks=%d (%d discovered from level.dat, %d sanitized), biomes=%d%n",
                blockNames.size(), discoveredIds.discovered, discoveredIds.sanitized, biomeNames.size());

        Path out = args.out.toAbsolutePath();
        if (Files.exists(out)) {
            if (!args.overwrite) {
                throw new IllegalArgumentException("Output exists; pass --overwrite: " + out);
            }
            Files.delete(out);
        }
        Files.createDirectories(out.getParent());

        List<RegionInfo> regions = findRegions(regionDir, centerChunkX, centerChunkZ, args.radiusChunks);
        if (args.maxRegionFiles > 0 && regions.size() > args.maxRegionFiles) {
            regions = new ArrayList<>(regions.subList(0, args.maxRegionFiles));
        }
        System.out.printf(Locale.ROOT, "regions selected=%d radiusChunks=%d%n", regions.size(), args.radiusChunks);

        long startNanos = System.nanoTime();
        ConversionStats stats = new ConversionStats();
        try (FullDataSourceV2Repo fullRepo = new FullDataSourceV2Repo(AbstractDhRepo.DEFAULT_DATABASE_TYPE, out.toFile());
             ChunkHashRepo hashRepo = new ChunkHashRepo(AbstractDhRepo.DEFAULT_DATABASE_TYPE, out.toFile())) {
            Connection fullConn = fullRepo.getConnection();
            Connection hashConn = hashRepo.getConnection();
            fullConn.setAutoCommit(false);
            hashConn.setAutoCommit(false);

            for (int i = 0; i < regions.size(); i++) {
                RegionInfo region = regions.get(i);
                RegionStats regionStats = convertRegion(region, centerChunkX, centerChunkZ, args.radiusChunks,
                        blockNames, biomeNames, args.compressionMode, fullRepo, hashRepo);
                fullConn.commit();
                hashConn.commit();

                stats.add(regionStats);
                if (args.progressEveryRegions > 0 && ((i + 1) % args.progressEveryRegions == 0 || i + 1 == regions.size())) {
                    double elapsedSeconds = (System.nanoTime() - startNanos) / 1_000_000_000.0;
                    System.out.printf(Locale.ROOT,
                            "progress regions=%d/%d chunks=%d sections=%d missingChunks=%d elapsed=%.1fs out=%.1f MiB%n",
                            i + 1, regions.size(), stats.chunks, stats.sections, stats.missingChunks,
                            elapsedSeconds, Files.size(out) / 1048576.0);
                }

                if (args.maxChunks > 0 && stats.chunks >= args.maxChunks) {
                    System.out.printf(Locale.ROOT, "stopping early at --max-chunks=%d%n", args.maxChunks);
                    break;
                }
            }
        }

        validateDatabase(out);
        if (args.buildParentLods) {
            buildParentLods(out, args.compressionMode);
            validateDatabase(out);
        }
        if (args.validationWorld != null) {
            createValidationWorld(save, out, args.validationWorld);
        }
        double elapsedSeconds = (System.nanoTime() - startNanos) / 1_000_000_000.0;
        System.out.printf(Locale.ROOT,
                "done chunks=%d sections=%d missingChunks=%d output=%s size=%.1f MiB elapsed=%.1fs%n",
                stats.chunks, stats.sections, stats.missingChunks, out, Files.size(out) / 1048576.0, elapsedSeconds);
    }

    private static void rebuildChunkHashes(Args args) throws Exception {
        if (args.save == null) {
            throw new IllegalArgumentException("Required with --rebuild-chunk-hashes-only: --save <world save>");
        }
        Path save = args.save.toAbsolutePath();
        Path sqlite = args.rebuildChunkHashesOnly.toAbsolutePath();
        Path regionDir = save.resolve("region");
        Path levelDat = save.resolve("level.dat");
        if (!Files.isDirectory(regionDir)) {
            throw new IllegalArgumentException("Missing region directory: " + regionDir);
        }
        if (!Files.isRegularFile(levelDat)) {
            throw new IllegalArgumentException("Missing level.dat: " + levelDat);
        }
        if (!Files.isRegularFile(sqlite)) {
            throw new IllegalArgumentException("Missing sqlite db: " + sqlite);
        }

        Nbt.Compound levelRoot = readLevelDat(levelDat);
        Nbt.Compound data = levelRoot.compound("Data");
        int spawnX = data.intValue("SpawnX", 0);
        int spawnZ = data.intValue("SpawnZ", 0);
        int centerChunkX = args.centerChunkSet ? args.centerChunkX : floorDiv(spawnX, CHUNK_WIDTH);
        int centerChunkZ = args.centerChunkSet ? args.centerChunkZ : floorDiv(spawnZ, CHUNK_WIDTH);
        Map<Integer, String> blockNames = vanillaBlockNames();
        DiscoveryStats discoveredIds = discoverForgeIds(levelRoot, blockNames);
        Map<Integer, String> biomeNames = biomeNames(save);
        List<RegionInfo> regions = findRegions(regionDir, centerChunkX, centerChunkZ, args.radiusChunks);
        if (args.maxRegionFiles > 0 && regions.size() > args.maxRegionFiles) {
            regions = new ArrayList<>(regions.subList(0, args.maxRegionFiles));
        }
        System.out.printf(Locale.ROOT,
                "rebuild chunk hashes sqlite=%s centerChunk=(%d,%d) radiusChunks=%d regions=%d blocks=%d (%d discovered, %d sanitized)%n",
                sqlite, centerChunkX, centerChunkZ, args.radiusChunks, regions.size(),
                blockNames.size(), discoveredIds.discovered, discoveredIds.sanitized);

        long startNanos = System.nanoTime();
        long chunks = 0;
        long missingChunks = 0;
        try (ChunkHashRepo hashRepo = new ChunkHashRepo(AbstractDhRepo.DEFAULT_DATABASE_TYPE, sqlite.toFile())) {
            Connection conn = hashRepo.getConnection();
            conn.setAutoCommit(false);
            try (Statement statement = conn.createStatement()) {
                statement.executeUpdate("DELETE FROM ChunkHash");
            }

            int radiusSquared = args.radiusChunks * args.radiusChunks;
            for (int i = 0; i < regions.size(); i++) {
                RegionInfo region = regions.get(i);
                try (AnvilRegion anvil = new AnvilRegion(region.path)) {
                    for (int localZ = 0; localZ < CHUNKS_PER_REGION; localZ++) {
                        for (int localX = 0; localX < CHUNKS_PER_REGION; localX++) {
                            int chunkX = region.regionX * CHUNKS_PER_REGION + localX;
                            int chunkZ = region.regionZ * CHUNKS_PER_REGION + localZ;
                            int dx = chunkX - centerChunkX;
                            int dz = chunkZ - centerChunkZ;
                            if (dx * dx + dz * dz > radiusSquared) {
                                continue;
                            }
                            Nbt.Compound root = anvil.readChunk(localX, localZ);
                            if (root == null) {
                                missingChunks++;
                                continue;
                            }
                            ChunkData chunk = ChunkData.from(root, chunkX, chunkZ);
                            ChunkHashDTO hash = new ChunkHashDTO(new DhChunkPos(chunk.chunkX, chunk.chunkZ),
                                    chunk.computeDhChunkHash(blockNames, biomeNames, true));
                            hashRepo.save(hash);
                            hash.close();
                            chunks++;
                        }
                    }
                }
                if (args.progressEveryRegions > 0 && ((i + 1) % args.progressEveryRegions == 0 || i + 1 == regions.size())) {
                    double elapsedSeconds = (System.nanoTime() - startNanos) / 1_000_000_000.0;
                    System.out.printf(Locale.ROOT,
                            "hash progress regions=%d/%d chunks=%d missingChunks=%d elapsed=%.1fs%n",
                            i + 1, regions.size(), chunks, missingChunks, elapsedSeconds);
                }
                conn.commit();
            }
        }
        double elapsedSeconds = (System.nanoTime() - startNanos) / 1_000_000_000.0;
        System.out.printf(Locale.ROOT,
                "rebuild chunk hashes done chunks=%d missingChunks=%d sqlite=%s elapsed=%.1fs%n",
                chunks, missingChunks, sqlite, elapsedSeconds);
    }

    private static RegionStats convertRegion(
            RegionInfo region,
            int centerChunkX,
            int centerChunkZ,
            int radiusChunks,
            Map<Integer, String> blockNames,
            Map<Integer, String> biomeNames,
            EDhApiDataCompressionMode compressionMode,
            FullDataSourceV2Repo fullRepo,
            ChunkHashRepo hashRepo) throws Exception {
        Map<Long, FullDataSourceV2> dhSections = new HashMap<>();
        List<ChunkHashDTO> chunkHashes = new ArrayList<>();
        RegionStats stats = new RegionStats();
        try (AnvilRegion anvil = new AnvilRegion(region.path)) {
            int radiusSquared = radiusChunks * radiusChunks;
            for (int localZ = 0; localZ < CHUNKS_PER_REGION; localZ++) {
                for (int localX = 0; localX < CHUNKS_PER_REGION; localX++) {
                    int chunkX = region.regionX * CHUNKS_PER_REGION + localX;
                    int chunkZ = region.regionZ * CHUNKS_PER_REGION + localZ;
                    int dx = chunkX - centerChunkX;
                    int dz = chunkZ - centerChunkZ;
                    if (dx * dx + dz * dz > radiusSquared) {
                        continue;
                    }

                    Nbt.Compound root = anvil.readChunk(localX, localZ);
                    if (root == null) {
                        stats.missingChunks++;
                        continue;
                    }

                    ChunkData chunk = ChunkData.from(root, chunkX, chunkZ);
                    int chunkHash = chunk.computeDhChunkHash(blockNames, biomeNames, true);
                    writeChunkToSections(chunk, dhSections, blockNames, biomeNames);
                    chunkHashes.add(new ChunkHashDTO(new DhChunkPos(chunk.chunkX, chunk.chunkZ), chunkHash));
                    stats.chunks++;
                }
            }
        }

        for (FullDataSourceV2 source : dhSections.values()) {
            if (source.mapping.isEmpty()) {
                throw new IllegalStateException("DH section has data but an empty id mapping before save: " + source);
            }
            cullHiddenDataPoints(source);
            normalizeColumnMetadata(source);
            source.applyToParent = Boolean.TRUE;
            source.applyToChildren = Boolean.FALSE;
            FullDataSourceV2DTO dto = FullDataSourceV2DTO.CreateFromDataSource(source, compressionMode);
            fullRepo.save(dto);
            dto.close();
            source.close();
            stats.sections++;
        }
        for (ChunkHashDTO hash : chunkHashes) {
            hashRepo.save(hash);
            hash.close();
        }
        return stats;
    }

    private static void writeChunkToSections(
            ChunkData chunk,
            Map<Long, FullDataSourceV2> dhSections,
            Map<Integer, String> blockNames,
            Map<Integer, String> biomeNames) {
        int sectionX = floorDiv(chunk.chunkX, CHUNKS_PER_DH_SECTION);
        int sectionZ = floorDiv(chunk.chunkZ, CHUNKS_PER_DH_SECTION);
        long sectionPos = DhSectionPos.encode(DhSectionPos.SECTION_BLOCK_DETAIL_LEVEL, sectionX, sectionZ);
        FullDataSourceV2 source = dhSections.computeIfAbsent(sectionPos, FullDataSourceV2::createEmpty);

        int chunkBaseRelX = floorMod(chunk.chunkX, CHUNKS_PER_DH_SECTION) * CHUNK_WIDTH;
        int chunkBaseRelZ = floorMod(chunk.chunkZ, CHUNKS_PER_DH_SECTION) * CHUNK_WIDTH;
        for (int z = 0; z < CHUNK_WIDTH; z++) {
            for (int x = 0; x < CHUNK_WIDTH; x++) {
                int biomeId = chunk.biomeAt(x, z);
                IBiomeWrapper biome = biomeWrapper(biomeNames.getOrDefault(biomeId, unknownBiomeName(biomeId)));
                LongArrayList points = chunkColumnPoints(source, chunk, x, z, biome, blockNames);
                source.setSingleColumn(points, chunkBaseRelX + x, chunkBaseRelZ + z,
                        EDhApiWorldGenerationStep.LIGHT, EDhApiWorldCompressionMode.VISUALLY_EQUAL);
            }
        }
    }

    private static void cullHiddenDataPoints(FullDataSourceV2 source) {
        for (int z = 0; z < SECTION_BLOCK_WIDTH; z++) {
            for (int x = 0; x < SECTION_BLOCK_WIDTH; x++) {
                LongArrayList column = source.tryGetColumnAtRelPos(x, z);
                if (column != null && column.size() > 1) {
                    FullDataOcclusionCuller.cullHiddenDatapointsInColumn(source, x, z);
                }
            }
        }
    }

    private static void normalizeColumnMetadata(FullDataSourceV2 source) {
        for (int z = 0; z < SECTION_BLOCK_WIDTH; z++) {
            for (int x = 0; x < SECTION_BLOCK_WIDTH; x++) {
                int columnIndex = FullDataSourceV2.relativePosToIndex(x, z);
                LongArrayList column = source.tryGetColumnAtRelPos(x, z);
                if (column == null || column.isEmpty()) {
                    source.columnGenerationSteps.set(columnIndex, EDhApiWorldGenerationStep.EMPTY.value);
                    source.columnWorldCompressionMode.set(columnIndex, EDhApiWorldCompressionMode.MERGE_SAME_BLOCKS.value);
                    continue;
                }
                source.columnGenerationSteps.set(columnIndex, EDhApiWorldGenerationStep.LIGHT.value);
                source.columnWorldCompressionMode.set(columnIndex, EDhApiWorldCompressionMode.VISUALLY_EQUAL.value);
            }
        }
    }

    private static LongArrayList chunkColumnPoints(
            FullDataSourceV2 source,
            ChunkData chunk,
            int x,
            int z,
            IBiomeWrapper biome,
            Map<Integer, String> blockNames) throws RuntimeException {
        List<Segment> bottomUp = new ArrayList<>();
        ColumnValue current = chunk.columnValue(x, WORLD_MIN_Y, z, blockNames);
        int bottom = WORLD_MIN_Y;
        for (int y = WORLD_MIN_Y + 1; y < WORLD_MAX_Y_EXCLUSIVE; y++) {
            ColumnValue value = chunk.columnValue(x, y, z, blockNames);
            if (!value.sameAs(current)) {
                bottomUp.add(new Segment(bottom, y, current));
                bottom = y;
                current = value;
            }
        }
        bottomUp.add(new Segment(bottom, WORLD_MAX_Y_EXCLUSIVE, current));

        LongArrayList topDown = new LongArrayList(bottomUp.size());
        for (int i = bottomUp.size() - 1; i >= 0; i--) {
            Segment segment = bottomUp.get(i);
            IBlockStateWrapper block = StaticBlockWrapper.of(segment.value.blockSerial);
            int id = source.mapping.addIfNotPresentAndGetId(biome, block);
            int height = segment.top - segment.bottom;
            ColumnValue light = chunk.renderLightForSegment(x, z, segment);
            try {
                topDown.add(FullDataPointUtil.encode(id, height, segment.bottom,
                        (byte) light.blockLight, (byte) light.skyLight));
            } catch (Exception ex) {
                throw new IllegalArgumentException("Unable to encode DH full data point at chunk "
                        + chunk.chunkX + "," + chunk.chunkZ + " rel " + x + "," + z
                        + " segment " + segment.bottom + ".." + segment.top, ex);
            }
        }
        return topDown;
    }

    private static void validateDatabase(Path sqlite) throws Exception {
        if (!Files.isRegularFile(sqlite)) {
            throw new IllegalArgumentException("Missing sqlite db: " + sqlite);
        }
        try (FullDataSourceV2Repo repo = new FullDataSourceV2Repo(AbstractDhRepo.DEFAULT_DATABASE_TYPE, sqlite.toFile())) {
            List<Long> positions = repo.getAllPositions();
            if (positions.isEmpty()) {
                throw new IllegalStateException("DH cache has no FullData rows: " + sqlite);
            }
            int decoded = 0;
            for (Long pos : positions) {
                FullDataSourceV2DTO dto = repo.getByKey(pos);
                if (dto == null) {
                    throw new IllegalStateException("Unable to reload FullData row " + pos);
                }
                try (FullDataSourceV2 source = dto.createDataSource(StaticLevelWrapper.INSTANCE, null)) {
                    if (source.getPos() != pos) {
                        throw new IllegalStateException("Decoded section pos mismatch for " + pos);
                    }
                }
                dto.close();
                if (++decoded >= Math.min(16, positions.size())) {
                    break;
                }
            }
            System.out.printf(Locale.ROOT, "validated DH sqlite rows=%d decodedSamples=%d path=%s%n",
                    positions.size(), decoded, sqlite.toAbsolutePath());
        }
    }

    private static void scanDatabaseMappings(Path sqlite) throws Exception {
        if (!Files.isRegularFile(sqlite)) {
            throw new IllegalArgumentException("Missing sqlite db: " + sqlite);
        }
        int rows = 0;
        int decoded = 0;
        long mappingEntries = 0;
        long controlStrings = 0;
        long emptyBiomes = 0;
        long unknownBiomes = 0;
        List<String> badSamples = new ArrayList<>();
        long startNanos = System.nanoTime();
        try (FullDataSourceV2Repo repo = new FullDataSourceV2Repo(AbstractDhRepo.DEFAULT_DATABASE_TYPE, sqlite.toFile())) {
            List<Long> positions = repo.getAllPositions();
            rows = positions.size();
            for (Long pos : positions) {
                FullDataSourceV2DTO dto = repo.getByKey(pos);
                if (dto == null) {
                    throw new IllegalStateException("Unable to reload FullData row " + pos);
                }
                try (FullDataSourceV2 source = dto.createDataSource(StaticLevelWrapper.INSTANCE, null)) {
                    decoded++;
                    int maxValidId = source.mapping.getMaxValidId();
                    for (int id = 0; id <= maxValidId; id++) {
                        String biomeSerial = source.mapping.getBiomeWrapper(id).getSerialString();
                        String blockSerial = source.mapping.getBlockStateWrapper(id).getSerialString();
                        mappingEntries++;
                        boolean hasControl = containsControlCharacter(biomeSerial)
                                || containsControlCharacter(blockSerial);
                        boolean hasEmptyBiome = "EMPTY".equals(biomeSerial);
                        if (hasControl) {
                            controlStrings++;
                        }
                        if (hasEmptyBiome) {
                            emptyBiomes++;
                        }
                        if (biomeSerial.startsWith("biome:Unknown_")) {
                            unknownBiomes++;
                        }
                        if ((hasControl || hasEmptyBiome) && badSamples.size() < 20) {
                            badSamples.add("pos=" + pos + " id=" + id + " " + biomeSerial + "|" + blockSerial);
                        }
                    }
                }
                dto.close();
            }
        }
        System.out.printf(Locale.ROOT,
                "scan mappings rows=%d decoded=%d mappingEntries=%d controlStrings=%d emptyBiomes=%d unknownBiomes=%d elapsed=%.1fs path=%s%n",
                rows, decoded, mappingEntries, controlStrings, emptyBiomes, unknownBiomes,
                (System.nanoTime() - startNanos) / 1_000_000_000.0,
                sqlite.toAbsolutePath());
        if (!badSamples.isEmpty()) {
            System.out.println("badMappingSamples=" + badSamples);
        }
    }

    private static void inspectDatabasePosition(Path sqlite, int dbDetailLevel, int posX, int posZ) throws Exception {
        if (!Files.isRegularFile(sqlite)) {
            throw new IllegalArgumentException("Missing sqlite db: " + sqlite);
        }
        byte sectionDetail = (byte) (DhSectionPos.SECTION_BLOCK_DETAIL_LEVEL + dbDetailLevel);
        long pos = DhSectionPos.encode(sectionDetail, posX, posZ);
        try (FullDataSourceV2Repo repo = new FullDataSourceV2Repo(AbstractDhRepo.DEFAULT_DATABASE_TYPE, sqlite.toFile())) {
            FullDataSourceV2DTO dto = repo.getByKey(pos);
            if (dto == null) {
                System.out.printf(Locale.ROOT,
                        "inspect missing dbDetail=%d sectionDetail=%d pos=(%d,%d) encoded=%d path=%s%n",
                        dbDetailLevel, sectionDetail, posX, posZ, pos, sqlite.toAbsolutePath());
                return;
            }
            try (FullDataSourceV2 source = dto.createDataSource(StaticLevelWrapper.INSTANCE, null)) {
                dto.close();

                int maxValidId = source.mapping.getMaxValidId();
                int invalidIds = 0;
                int totalPoints = 0;
                int nonEmptyColumns = 0;
                int maxColumnPoints = 0;
                int maxColumnX = 0;
                int maxColumnZ = 0;
                int maxId = Integer.MIN_VALUE;
                int minId = Integer.MAX_VALUE;
                int minBottom = Integer.MAX_VALUE;
                int maxTop = Integer.MIN_VALUE;
                Map<Integer, Integer> columnSizeCounts = new TreeMap<>();
                Map<Integer, Integer> generationStepCounts = new TreeMap<>();
                Map<Integer, Integer> worldCompressionModeCounts = new TreeMap<>();
                List<String> invalidSamples = new ArrayList<>();
                List<String> largestColumns = new ArrayList<>();
                List<String> mappingSamples = new ArrayList<>();
                int mappingControlStrings = 0;

                for (int id = 0; id <= maxValidId; id++) {
                    String biomeSerial = source.mapping.getBiomeWrapper(id).getSerialString();
                    String blockSerial = source.mapping.getBlockStateWrapper(id).getSerialString();
                    if (containsControlCharacter(biomeSerial) || containsControlCharacter(blockSerial)) {
                        mappingControlStrings++;
                    }
                    if (mappingSamples.size() < 20) {
                        mappingSamples.add(id + "=" + biomeSerial + "|" + blockSerial);
                    }
                }

                for (int z = 0; z < SECTION_BLOCK_WIDTH; z++) {
                    for (int x = 0; x < SECTION_BLOCK_WIDTH; x++) {
                        int columnIndex = FullDataSourceV2.relativePosToIndex(x, z);
                        generationStepCounts.merge((int) source.columnGenerationSteps.getByte(columnIndex), 1, Integer::sum);
                        worldCompressionModeCounts.merge((int) source.columnWorldCompressionMode.getByte(columnIndex), 1, Integer::sum);
                        LongArrayList column = source.tryGetColumnAtRelPos(x, z);
                        int size = column == null ? 0 : column.size();
                        columnSizeCounts.merge(size, 1, Integer::sum);
                        if (size > 0) {
                            nonEmptyColumns++;
                        }
                        if (size > maxColumnPoints) {
                            maxColumnPoints = size;
                            maxColumnX = x;
                            maxColumnZ = z;
                        }
                        if (largestColumns.size() < 12 || size > smallestColumnSampleSize(largestColumns)) {
                            largestColumns.add(x + "," + z + "=" + size);
                            largestColumns.sort((a, b) -> Integer.compare(columnSampleSize(b), columnSampleSize(a)));
                            if (largestColumns.size() > 12) {
                                largestColumns.remove(largestColumns.size() - 1);
                            }
                        }
                        if (column == null) {
                            continue;
                        }
                        for (int i = 0; i < column.size(); i++) {
                            long point = column.getLong(i);
                            int id = FullDataPointUtil.getId(point);
                            int bottom = FullDataPointUtil.getBottomY(point);
                            int height = FullDataPointUtil.getHeight(point);
                            int top = bottom + height;
                            totalPoints++;
                            maxId = Math.max(maxId, id);
                            minId = Math.min(minId, id);
                            minBottom = Math.min(minBottom, bottom);
                            maxTop = Math.max(maxTop, top);
                            if (id < 0 || id > maxValidId) {
                                invalidIds++;
                                if (invalidSamples.size() < 12) {
                                    invalidSamples.add("col=" + x + "," + z + " index=" + i + " id=" + id
                                            + " point=" + FullDataPointUtil.toString(point));
                                }
                            }
                        }
                    }
                }

                System.out.printf(Locale.ROOT,
                        "inspect dbDetail=%d sectionDetail=%d pos=(%d,%d) encoded=%d mappingSize=%d maxValidId=%d "
                                + "columns=%d nonEmpty=%d points=%d invalidIds=%d idRange=%d..%d yRange=%d..%d "
                                + "maxColumn=%d at=(%d,%d) applyToParent=%s applyToChildren=%s empty=%s%n",
                        dbDetailLevel, sectionDetail, posX, posZ, pos, source.mapping.size(), maxValidId,
                        SECTION_BLOCK_WIDTH * SECTION_BLOCK_WIDTH, nonEmptyColumns, totalPoints, invalidIds,
                        minId == Integer.MAX_VALUE ? -1 : minId,
                        maxId == Integer.MIN_VALUE ? -1 : maxId,
                        minBottom == Integer.MAX_VALUE ? -1 : minBottom,
                        maxTop == Integer.MIN_VALUE ? -1 : maxTop,
                        maxColumnPoints, maxColumnX, maxColumnZ,
                        source.applyToParent, source.applyToChildren, source.isEmpty);
                System.out.println("mappingControlStrings=" + mappingControlStrings);
                System.out.println("mappingSamples=" + mappingSamples);
                System.out.println("columnSizeCounts=" + columnSizeCounts);
                System.out.println("generationStepCounts=" + generationStepCounts);
                System.out.println("worldCompressionModeCounts=" + worldCompressionModeCounts);
                System.out.println("largestColumns=" + largestColumns);
                if (!invalidSamples.isEmpty()) {
                    System.out.println("invalidSamples=" + invalidSamples);
                }
            }
        }
    }

    private static void inspectSaveChunk(Path save, int chunkX, int chunkZ) throws Exception {
        Map<Integer, String> blockNames = vanillaBlockNames();
        discoverForgeIds(readLevelDat(save.resolve("level.dat")), blockNames);
        Path regionPath = save.resolve("region").resolve("r." + floorDiv(chunkX, CHUNKS_PER_REGION)
                + "." + floorDiv(chunkZ, CHUNKS_PER_REGION) + ".mca");
        try (AnvilRegion region = new AnvilRegion(regionPath)) {
            Nbt.Compound root = region.readChunk(floorMod(chunkX, CHUNKS_PER_REGION), floorMod(chunkZ, CHUNKS_PER_REGION));
            if (root == null) {
                System.out.printf(Locale.ROOT, "inspect chunk missing chunk=(%d,%d) region=%s%n", chunkX, chunkZ, regionPath);
                return;
            }
            Nbt.ListTag rawSections = root.compound("Level").list("Sections");
            if (rawSections != null && !rawSections.values.isEmpty() && rawSections.values.get(0) instanceof Nbt.Compound first) {
                Map<String, String> rawTypes = new TreeMap<>();
                for (Map.Entry<String, Object> entry : first.entrySet()) {
                    rawTypes.put(entry.getKey(), describeNbtValue(entry.getValue()));
                }
                System.out.println("firstSectionRaw=" + rawTypes);
                byte[] blocks16 = first.byteArray("Blocks16");
                if (blocks16 != null) {
                    System.out.println("firstSectionBlocks16BigEndian=" + shortCounts(blocks16, true, 12));
                    System.out.println("firstSectionBlocks16LittleEndian=" + shortCounts(blocks16, false, 12));
                }
            }
            ChunkData chunk = ChunkData.from(root, chunkX, chunkZ);
            Map<Integer, Integer> blockCounts = new TreeMap<>();
            Map<String, Integer> serialCounts = new TreeMap<>();
            for (ChunkSection section : chunk.sections.values()) {
                for (int index = 0; index < 4096; index++) {
                    int id = section.blockId(index);
                    int meta = nibble(section.data, index, 0);
                    blockCounts.merge(id, 1, Integer::sum);
                    serialCounts.merge(blockSerial(blockNames, id, meta), 1, Integer::sum);
                }
            }
            System.out.printf(Locale.ROOT,
                    "inspect chunk=(%d,%d) sections=%s blockIds=%s serials(first20)=%s%n",
                    chunk.chunkX, chunk.chunkZ, new TreeSet<>(chunk.sections.keySet()), blockCounts,
                    firstEntries(serialCounts, 20));
        }
    }

    private static String describeNbtValue(Object value) {
        if (value == null) {
            return "null";
        }
        if (value instanceof byte[] bytes) {
            return "byte[" + bytes.length + "]";
        }
        if (value instanceof int[] ints) {
            return "int[" + ints.length + "]";
        }
        if (value instanceof long[] longs) {
            return "long[" + longs.length + "]";
        }
        if (value instanceof Nbt.ListTag list) {
            return "list[" + list.values.size() + "]";
        }
        if (value instanceof Nbt.Compound compound) {
            return "compound[" + compound.size() + "]";
        }
        return value.getClass().getSimpleName() + "=" + value;
    }

    private static Map<Integer, Integer> shortCounts(byte[] bytes, boolean bigEndian, int limit) {
        Map<Integer, Integer> counts = new TreeMap<>();
        for (int i = 0; i + 1 < bytes.length; i += 2) {
            int first = bytes[i] & 0xFF;
            int second = bytes[i + 1] & 0xFF;
            int value = bigEndian ? (first << 8) | second : first | (second << 8);
            counts.merge(value, 1, Integer::sum);
        }
        Map<Integer, Integer> top = new LinkedHashMap<>();
        counts.entrySet().stream()
                .sorted((a, b) -> Integer.compare(b.getValue(), a.getValue()))
                .limit(limit)
                .forEach(entry -> top.put(entry.getKey(), entry.getValue()));
        return top;
    }

    private static Map<String, Integer> firstEntries(Map<String, Integer> input, int limit) {
        Map<String, Integer> output = new LinkedHashMap<>();
        int count = 0;
        for (Map.Entry<String, Integer> entry : input.entrySet()) {
            output.put(entry.getKey(), entry.getValue());
            if (++count >= limit) {
                break;
            }
        }
        return output;
    }

    private static int smallestColumnSampleSize(List<String> samples) {
        int smallest = Integer.MAX_VALUE;
        for (String sample : samples) {
            smallest = Math.min(smallest, columnSampleSize(sample));
        }
        return smallest;
    }

    private static int columnSampleSize(String sample) {
        int eq = sample.lastIndexOf('=');
        return eq < 0 ? 0 : Integer.parseInt(sample.substring(eq + 1));
    }

    private static void buildParentLods(Path sqlite, EDhApiDataCompressionMode compressionMode) throws Exception {
        if (!Files.isRegularFile(sqlite)) {
            throw new IllegalArgumentException("Missing sqlite db: " + sqlite);
        }
        long startNanos = System.nanoTime();
        int totalParents = 0;
        try (FullDataSourceV2Repo repo = new FullDataSourceV2Repo(AbstractDhRepo.DEFAULT_DATABASE_TYPE, sqlite.toFile())) {
            Connection conn = repo.getConnection();
            conn.setAutoCommit(false);
            for (byte parentDetail = (byte) (DhSectionPos.SECTION_BLOCK_DETAIL_LEVEL + 1);
                 parentDetail <= 15;
                 parentDetail++) {
                Set<Long> childPositions = positionsAtDetail(repo, (byte) (parentDetail - 1));
                if (childPositions.isEmpty()) {
                    System.out.printf(Locale.ROOT,
                            "parent-lods detail=%d skipped no children%n",
                            parentDetail - DhSectionPos.SECTION_BLOCK_DETAIL_LEVEL);
                    continue;
                }

                Set<Long> parentPositions = new TreeSet<>();
                for (long childPos : childPositions) {
                    parentPositions.add(DhSectionPos.getParentPos(childPos));
                }

                int savedParents = 0;
                int updatedFromChildren = 0;
                for (long parentPos : parentPositions) {
                    FullDataSourceV2 parent = FullDataSourceV2.createEmpty(parentPos);
                    FullDataSourceV2DTO existingParentDto = repo.getByKey(parentPos);
                    if (existingParentDto != null) {
                        try (FullDataSourceV2 existingParent = existingParentDto.createDataSource(StaticLevelWrapper.INSTANCE, null)) {
                            parent.updateFromDataSource(existingParent);
                        }
                        existingParentDto.close();
                    }

                    int childrenForParent = 0;
                    for (int childIndex = 0; childIndex < 4; childIndex++) {
                        long childPos = DhSectionPos.getChildByIndex(parentPos, childIndex);
                        if (!childPositions.contains(childPos)) {
                            continue;
                        }
                        FullDataSourceV2DTO childDto = repo.getByKey(childPos);
                        if (childDto == null) {
                            continue;
                        }
                        try (FullDataSourceV2 childSource = childDto.createDataSource(StaticLevelWrapper.INSTANCE, null)) {
                            parent.updateFromDataSource(childSource);
                        }
                        childDto.close();
                        updatedFromChildren++;
                        childrenForParent++;
                    }

                    if (childrenForParent > 0) {
                        cullHiddenDataPoints(parent);
                        normalizeColumnMetadata(parent);
                        parent.applyToParent = parentDetail < 15;
                        parent.applyToChildren = parentDetail > DhSectionPos.SECTION_BLOCK_DETAIL_LEVEL;
                        FullDataSourceV2DTO parentDto = FullDataSourceV2DTO.CreateFromDataSource(parent, compressionMode);
                        repo.save(parentDto);
                        parentDto.close();
                        savedParents++;
                        if (savedParents % 1000 == 0) {
                            System.out.printf(Locale.ROOT,
                                    "parent-lods detail=%d progress saved=%d/%d childUpdates=%d elapsed=%.1fs%n",
                                    parentDetail - DhSectionPos.SECTION_BLOCK_DETAIL_LEVEL,
                                    savedParents,
                                    parentPositions.size(),
                                    updatedFromChildren,
                                    (System.nanoTime() - startNanos) / 1_000_000_000.0);
                        }
                    }
                    parent.close();
                }

                conn.commit();
                totalParents += savedParents;
                System.out.printf(Locale.ROOT,
                        "parent-lods detail=%d parents=%d saved=%d childUpdates=%d elapsed=%.1fs%n",
                        parentDetail - DhSectionPos.SECTION_BLOCK_DETAIL_LEVEL,
                        parentPositions.size(),
                        savedParents,
                        updatedFromChildren,
                        (System.nanoTime() - startNanos) / 1_000_000_000.0);
            }
        }
        System.out.printf(Locale.ROOT,
                "parent-lods done saved=%d path=%s elapsed=%.1fs%n",
                totalParents,
                sqlite.toAbsolutePath(),
                (System.nanoTime() - startNanos) / 1_000_000_000.0);
    }

    private static Set<Long> positionsAtDetail(FullDataSourceV2Repo repo, byte sectionDetail) {
        Set<Long> positions = new HashSet<>();
        for (Long pos : repo.getAllPositions()) {
            if (DhSectionPos.getDetailLevel(pos) == sectionDetail) {
                positions.add(pos);
            }
        }
        return positions;
    }

    private static void createValidationWorld(Path sourceSave, Path sqlite, Path validationWorld) throws IOException {
        Files.createDirectories(validationWorld.resolve("data"));
        Files.copy(sourceSave.resolve("level.dat"), validationWorld.resolve("level.dat"),
                java.nio.file.StandardCopyOption.REPLACE_EXISTING);
        Path levelDatOld = sourceSave.resolve("level.dat_old");
        if (Files.isRegularFile(levelDatOld)) {
            Files.copy(levelDatOld, validationWorld.resolve("level.dat_old"),
                    java.nio.file.StandardCopyOption.REPLACE_EXISTING);
        }
        Files.copy(sqlite, validationWorld.resolve("data").resolve("DistantHorizons.sqlite"),
                java.nio.file.StandardCopyOption.REPLACE_EXISTING);
        Files.writeString(validationWorld.resolve("dh-cache-converter.txt"),
                "Created by DhCacheChunkConverter at " + Instant.now() + System.lineSeparator()
                        + "sourceSave=" + sourceSave.toAbsolutePath() + System.lineSeparator()
                        + "sqlite=" + sqlite.toAbsolutePath() + System.lineSeparator(),
                StandardCharsets.UTF_8);
        System.out.println("validation world staged: " + validationWorld.toAbsolutePath());
    }

    private static Nbt.Compound readLevelDat(Path levelDat) throws IOException {
        try (InputStream in = new GZIPInputStream(new BufferedInputStream(Files.newInputStream(levelDat)))) {
            Object root = Nbt.readNamed(in).value;
            if (!(root instanceof Nbt.Compound compound)) {
                throw new IOException("level.dat root is not a compound");
            }
            return compound;
        }
    }

    private static List<RegionInfo> findRegions(Path regionDir, int centerChunkX, int centerChunkZ, int radiusChunks) throws IOException {
        List<RegionInfo> regions = new ArrayList<>();
        try (var paths = Files.list(regionDir)) {
            paths.filter(path -> path.getFileName().toString().startsWith("r.") && path.getFileName().toString().endsWith(".mca"))
                    .forEach(path -> {
                        RegionInfo info = RegionInfo.parse(path);
                        if (info != null && info.intersects(centerChunkX, centerChunkZ, radiusChunks)) {
                            regions.add(info);
                        }
                    });
        }
        regions.sort(Comparator.comparingLong(region -> region.distanceSquared(centerChunkX, centerChunkZ)));
        return regions;
    }

    private static DiscoveryStats discoverForgeIds(Nbt.Compound root, Map<Integer, String> blockNames) {
        int before = blockNames.size();
        DiscoveryStats stats = new DiscoveryStats();
        discoverForgeIdsRecursive(root, blockNames, stats);
        stats.discovered = blockNames.size() - before;
        return stats;
    }

    @SuppressWarnings("unchecked")
    private static void discoverForgeIdsRecursive(Object value, Map<Integer, String> blockNames, DiscoveryStats stats) {
        if (value instanceof Nbt.Compound compound) {
            maybeAddForgeId(compound, blockNames, stats);
            for (Object child : compound.values()) {
                discoverForgeIdsRecursive(child, blockNames, stats);
            }
        } else if (value instanceof Nbt.ListTag list) {
            for (Object child : list.values) {
                discoverForgeIdsRecursive(child, blockNames, stats);
            }
        }
    }

    private static void maybeAddForgeId(Nbt.Compound compound, Map<Integer, String> blockNames, DiscoveryStats stats) {
        String name = null;
        Integer id = null;
        for (String nameKey : List.of("K", "Name", "name", "Block", "block")) {
            Object candidate = compound.get(nameKey);
            if (candidate instanceof String s && looksLikeRegistryName(s)) {
                String normalized = normalizeRegistryName(s);
                if (!normalized.equals(s)) {
                    stats.sanitized++;
                }
                name = normalized;
                break;
            }
        }
        for (String idKey : List.of("V", "Id", "id", "ID")) {
            Object candidate = compound.get(idKey);
            if (candidate instanceof Number n) {
                id = n.intValue();
                break;
            }
        }
        if (name != null && id != null && id >= 0 && id < 4096) {
            blockNames.putIfAbsent(id, name);
        }
    }

    private static boolean looksLikeRegistryName(String name) {
        String normalized = normalizeRegistryName(name);
        return !normalized.isBlank()
                && !normalized.contains(" ")
                && !normalized.startsWith("item.")
                && (normalized.contains(":") || normalized.startsWith("tile."));
    }

    private static String normalizeRegistryName(String name) {
        name = stripControlCharacters(name).trim();
        if (name.startsWith("tile.")) {
            name = name.substring("tile.".length());
        }
        if (!name.contains(":")) {
            name = "minecraft:" + name;
        }
        return name;
    }

    private static Map<Integer, String> biomeNames(Path save) {
        Map<Integer, String> names = vanillaBiomeNames();
        Path mcDir = findMinecraftDirectory(save);
        if (mcDir != null) {
            Path bopIds = mcDir.resolve("config").resolve("biomesoplenty").resolve("ids.cfg");
            if (Files.isRegularFile(bopIds)) {
                parseBiomeIdsConfig(bopIds, names);
            }
        }
        return names;
    }

    private static Path findMinecraftDirectory(Path save) {
        Path current = save.toAbsolutePath();
        for (int i = 0; i < 8 && current != null; i++) {
            if (Files.isDirectory(current.resolve("config")) && Files.isDirectory(current.resolve("mods"))) {
                return current;
            }
            if (".minecraft".equals(current.getFileName() == null ? "" : current.getFileName().toString())) {
                return current;
            }
            current = current.getParent();
        }
        return null;
    }

    private static String stripControlCharacters(String value) {
        StringBuilder out = new StringBuilder(value.length());
        for (int i = 0; i < value.length(); i++) {
            char ch = value.charAt(i);
            if (!Character.isISOControl(ch)) {
                out.append(ch);
            }
        }
        return out.toString();
    }

    private static boolean containsControlCharacter(String value) {
        for (int i = 0; i < value.length(); i++) {
            if (Character.isISOControl(value.charAt(i))) {
                return true;
            }
        }
        return false;
    }

    private static void parseBiomeIdsConfig(Path config, Map<Integer, String> names) {
        try {
            boolean inBiomeIdsBlock = false;
            for (String rawLine : Files.readAllLines(config, StandardCharsets.UTF_8)) {
                String line = rawLine.trim();
                if (line.startsWith("#") || line.isBlank()) {
                    continue;
                }
                String lowerLine = line.toLowerCase(Locale.ROOT);
                if (lowerLine.startsWith("\"biome ids\"")) {
                    inBiomeIdsBlock = true;
                    continue;
                }
                if (line.startsWith("}")) {
                    inBiomeIdsBlock = false;
                    continue;
                }
                if (!line.contains("=")) {
                    continue;
                }
                int eq = line.indexOf('=');
                String key = line.substring(0, eq).trim();
                String value = line.substring(eq + 1).trim();
                if (!inBiomeIdsBlock && !key.toLowerCase(Locale.ROOT).contains("biome")) {
                    continue;
                }
                try {
                    int id = Integer.parseInt(value.replaceAll("[^0-9-]", ""));
                    String biomeName = key.replaceFirst("^[IBS]:", "")
                            .replace('"', ' ')
                            .replace("Biome ID", "")
                            .replace("biome id", "")
                            .replace("ID", "")
                            .trim();
                    if (!biomeName.isBlank() && id >= 0 && id < 256) {
                        names.putIfAbsent(id, biomeName);
                    }
                } catch (NumberFormatException ignored) {
                    // Forge config files can contain expressions and comments; ignore non-integer values.
                }
            }
        } catch (IOException ignored) {
            // Biome names are advisory for DH colors; EMPTY remains valid when a mod biome is unknown.
        }
    }

    private static IBiomeWrapper biomeWrapper(String name) {
        if (name == null || name.isBlank() || "EMPTY".equals(name)) {
            return EMPTY_BIOME;
        }
        if (name.startsWith("biome:")) {
            return StaticBiomeWrapper.of(name);
        }
        return StaticBiomeWrapper.of("biome:" + name);
    }

    private static String unknownBiomeName(int biomeId) {
        return "Unknown_" + biomeId;
    }

    private static int nibble(byte[] array, int index, int fallback) {
        if (array == null || index / 2 >= array.length) {
            return fallback;
        }
        int value = array[index / 2] & 0xFF;
        return (index & 1) == 0 ? (value & 0x0F) : ((value >>> 4) & 0x0F);
    }

    private static int floorDiv(int x, int y) {
        return Math.floorDiv(x, y);
    }

    private static int floorMod(int x, int y) {
        return Math.floorMod(x, y);
    }

    private static String blockSerial(Map<Integer, String> blockNames, int id, int meta) {
        String name = blockNames.get(id);
        if (name == null) {
            name = "minecraft:stone";
        } else {
            name = normalizeRegistryName(name);
        }
        if (id == 0 || "AIR".equals(name) || "minecraft:air".equals(name)) {
            return "AIR";
        }
        return meta == 0 ? name : name + ":" + meta;
    }

    private static Map<Integer, String> vanillaBlockNames() {
        Map<Integer, String> names = new HashMap<>();
        String[] vanilla = {
                "air", "stone", "grass", "dirt", "cobblestone", "planks", "sapling", "bedrock",
                "flowing_water", "water", "flowing_lava", "lava", "sand", "gravel", "gold_ore", "iron_ore",
                "coal_ore", "log", "leaves", "sponge", "glass", "lapis_ore", "lapis_block", "dispenser",
                "sandstone", "noteblock", "bed", "golden_rail", "detector_rail", "sticky_piston", "web", "tallgrass",
                "deadbush", "piston", "piston_head", "wool", "piston_extension", "yellow_flower", "red_flower",
                "brown_mushroom", "red_mushroom", "gold_block", "iron_block", "double_stone_slab", "stone_slab",
                "brick_block", "tnt", "bookshelf", "mossy_cobblestone", "obsidian", "torch", "fire", "mob_spawner",
                "oak_stairs", "chest", "redstone_wire", "diamond_ore", "diamond_block", "crafting_table", "wheat",
                "farmland", "furnace", "lit_furnace", "standing_sign", "wooden_door", "ladder", "rail", "stone_stairs",
                "wall_sign", "lever", "stone_pressure_plate", "iron_door", "wooden_pressure_plate", "redstone_ore",
                "lit_redstone_ore", "unlit_redstone_torch", "redstone_torch", "stone_button", "snow_layer", "ice",
                "snow", "cactus", "clay", "reeds", "jukebox", "fence", "pumpkin", "netherrack", "soul_sand",
                "glowstone", "portal", "lit_pumpkin", "cake", "unpowered_repeater", "powered_repeater", "stained_glass",
                "trapdoor", "monster_egg", "stonebrick", "brown_mushroom_block", "red_mushroom_block", "iron_bars",
                "glass_pane", "melon_block", "pumpkin_stem", "melon_stem", "vine", "fence_gate", "brick_stairs",
                "stone_brick_stairs", "mycelium", "waterlily", "nether_brick", "nether_brick_fence", "nether_brick_stairs",
                "nether_wart", "enchanting_table", "brewing_stand", "cauldron", "end_portal", "end_portal_frame",
                "end_stone", "dragon_egg", "redstone_lamp", "lit_redstone_lamp", "double_wooden_slab", "wooden_slab",
                "cocoa", "sandstone_stairs", "emerald_ore", "ender_chest", "tripwire_hook", "tripwire", "emerald_block",
                "spruce_stairs", "birch_stairs", "jungle_stairs", "command_block", "beacon", "cobblestone_wall",
                "flower_pot", "carrots", "potatoes", "wooden_button", "skull", "anvil", "trapped_chest",
                "light_weighted_pressure_plate", "heavy_weighted_pressure_plate", "unpowered_comparator",
                "powered_comparator", "daylight_detector", "redstone_block", "quartz_ore", "hopper", "quartz_block",
                "quartz_stairs", "activator_rail", "dropper", "stained_hardened_clay", "stained_glass_pane", "leaves2",
                "log2", "acacia_stairs", "dark_oak_stairs", "slime", "barrier", "iron_trapdoor", "prismarine",
                "sea_lantern", "hay_block", "carpet", "hardened_clay", "coal_block", "packed_ice", "double_plant"
        };
        for (int i = 0; i < vanilla.length; i++) {
            names.put(i, i == 0 ? "AIR" : "minecraft:" + vanilla[i]);
        }
        return names;
    }

    private static Map<Integer, String> vanillaBiomeNames() {
        Map<Integer, String> names = new HashMap<>();
        names.put(0, "Ocean");
        names.put(1, "Plains");
        names.put(2, "Desert");
        names.put(3, "Extreme Hills");
        names.put(4, "Forest");
        names.put(5, "Taiga");
        names.put(6, "Swampland");
        names.put(7, "River");
        names.put(8, "Hell");
        names.put(9, "Sky");
        names.put(10, "FrozenOcean");
        names.put(11, "FrozenRiver");
        names.put(12, "Ice Plains");
        names.put(13, "Ice Mountains");
        names.put(14, "MushroomIsland");
        names.put(15, "MushroomIslandShore");
        names.put(16, "Beach");
        names.put(17, "DesertHills");
        names.put(18, "ForestHills");
        names.put(19, "TaigaHills");
        names.put(20, "Extreme Hills Edge");
        names.put(21, "Jungle");
        names.put(22, "JungleHills");
        names.put(23, "JungleEdge");
        names.put(24, "Deep Ocean");
        names.put(25, "Stone Beach");
        names.put(26, "Cold Beach");
        names.put(27, "Birch Forest");
        names.put(28, "Birch Forest Hills");
        names.put(29, "Roofed Forest");
        names.put(30, "Cold Taiga");
        names.put(31, "Cold Taiga Hills");
        names.put(32, "Mega Taiga");
        names.put(33, "Mega Taiga Hills");
        names.put(34, "Extreme Hills+");
        names.put(35, "Savanna");
        names.put(36, "Savanna Plateau");
        names.put(37, "Mesa");
        names.put(38, "Mesa Plateau F");
        names.put(39, "Mesa Plateau");
        return names;
    }

    private record RegionInfo(Path path, int regionX, int regionZ) {
        static RegionInfo parse(Path path) {
            String name = path.getFileName().toString();
            String[] parts = name.split("\\.");
            if (parts.length != 4) {
                return null;
            }
            try {
                return new RegionInfo(path, Integer.parseInt(parts[1]), Integer.parseInt(parts[2]));
            } catch (NumberFormatException ex) {
                return null;
            }
        }

        boolean intersects(int centerChunkX, int centerChunkZ, int radiusChunks) {
            int minX = regionX * CHUNKS_PER_REGION;
            int maxX = minX + CHUNKS_PER_REGION - 1;
            int minZ = regionZ * CHUNKS_PER_REGION;
            int maxZ = minZ + CHUNKS_PER_REGION - 1;
            int closestX = Math.max(minX, Math.min(centerChunkX, maxX));
            int closestZ = Math.max(minZ, Math.min(centerChunkZ, maxZ));
            long dx = (long) closestX - centerChunkX;
            long dz = (long) closestZ - centerChunkZ;
            return dx * dx + dz * dz <= (long) radiusChunks * radiusChunks;
        }

        long distanceSquared(int centerChunkX, int centerChunkZ) {
            int centerX = regionX * CHUNKS_PER_REGION + CHUNKS_PER_REGION / 2;
            int centerZ = regionZ * CHUNKS_PER_REGION + CHUNKS_PER_REGION / 2;
            long dx = (long) centerX - centerChunkX;
            long dz = (long) centerZ - centerChunkZ;
            return dx * dx + dz * dz;
        }
    }

    private static final class RegionStats {
        long chunks;
        long sections;
        long missingChunks;
    }

    private static final class ConversionStats {
        long chunks;
        long sections;
        long missingChunks;

        void add(RegionStats region) {
            chunks += region.chunks;
            sections += region.sections;
            missingChunks += region.missingChunks;
        }
    }

    private static final class DiscoveryStats {
        int discovered;
        int sanitized;
    }

    private record Segment(int bottom, int top, ColumnValue value) {
    }

    private record ColumnValue(String blockSerial, int skyLight, int blockLight) {
        boolean sameAs(ColumnValue other) {
            return skyLight == other.skyLight
                    && blockLight == other.blockLight
                    && blockSerial.equals(other.blockSerial);
        }
    }

    private static final class ChunkData {
        final int chunkX;
        final int chunkZ;
        final Map<Integer, ChunkSection> sections;
        final byte[] biomes;
        final int[] heightMap;

        private ChunkData(int chunkX, int chunkZ, Map<Integer, ChunkSection> sections, byte[] biomes, int[] heightMap) {
            this.chunkX = chunkX;
            this.chunkZ = chunkZ;
            this.sections = sections;
            this.biomes = biomes == null ? new byte[256] : biomes;
            this.heightMap = heightMap == null ? new int[256] : heightMap;
        }

        static ChunkData from(Nbt.Compound root, int fallbackChunkX, int fallbackChunkZ) {
            Nbt.Compound level = root.compound("Level");
            int chunkX = level.intValue("xPos", fallbackChunkX);
            int chunkZ = level.intValue("zPos", fallbackChunkZ);
            Map<Integer, ChunkSection> sections = new HashMap<>();
            Nbt.ListTag sectionList = level.list("Sections");
            if (sectionList != null) {
                for (Object object : sectionList.values) {
                    if (object instanceof Nbt.Compound section) {
                        ChunkSection chunkSection = ChunkSection.from(section);
                        sections.put(chunkSection.y, chunkSection);
                    }
                }
            }
            return new ChunkData(chunkX, chunkZ, sections, level.byteArray("Biomes"), level.intArray("HeightMap"));
        }

        int biomeAt(int x, int z) {
            int index = z * CHUNK_WIDTH + x;
            return index >= 0 && index < biomes.length ? biomes[index] & 0xFF : 1;
        }

        ColumnValue columnValue(int x, int y, int z, Map<Integer, String> blockNames) {
            ChunkSection section = sections.get(y >> 4);
            if (section == null) {
                return new ColumnValue("AIR", 15, 0);
            }
            int localY = y & 15;
            int index = (localY << 8) | (z << 4) | x;
            int id = section.blockId(index);
            int meta = section.blockMeta(index);
            int blockLight = nibble(section.blockLight, index, 0);
            int skyLight = nibble(section.skyLight, index, id == 0 ? 15 : 0);
            return new ColumnValue(blockSerial(blockNames, id, meta), skyLight, blockLight);
        }

        ColumnValue renderLightForSegment(int x, int z, Segment segment) {
            if ("AIR".equals(segment.value.blockSerial)) {
                return segment.value;
            }
            ColumnValue above = columnValue(x, segment.top, z, Map.of());
            if (!"AIR".equals(above.blockSerial)) {
                return segment.value;
            }
            return new ColumnValue(segment.value.blockSerial,
                    Math.max(segment.value.skyLight, above.skyLight),
                    Math.max(segment.value.blockLight, above.blockLight));
        }

        int computeDhChunkHash(Map<Integer, String> blockNames, Map<Integer, String> biomeNames, boolean useEmptyBiomes) {
            int result = 31;
            final int blockPrime = 227;
            final int biomePrime = 701;
            final int yPrime = 137;

            IBlockStateWrapper previousBlock = null;
            for (int x = 0; x < CHUNK_WIDTH; x += 2) {
                for (int z = 0; z < CHUNK_WIDTH; z += 2) {
                    for (int y = 0; y < 255; y += 2) {
                        previousBlock = blockWrapperAt(x, y, z, blockNames, previousBlock);
                        result = result * blockPrime + previousBlock.hashCode();
                        result = result * biomePrime + biomeHashAt(x, z, biomeNames, useEmptyBiomes);
                        result = result * yPrime + y;
                    }
                }
            }

            for (int x = 0; x < CHUNK_WIDTH; x++) {
                for (int z = 0; z < CHUNK_WIDTH; z++) {
                    int lightBlockingHeight = lightBlockingHeightMapValue(x, z);
                    previousBlock = blockWrapperAt(x, lightBlockingHeight, z, blockNames, previousBlock);
                    result = result * blockPrime + previousBlock.hashCode();
                    result = result * biomePrime + biomeHashAt(x, z, biomeNames, useEmptyBiomes);
                    result = result * yPrime + lightBlockingHeight;

                    int solidHeight = 255;
                    if (solidHeight != lightBlockingHeight) {
                        previousBlock = blockWrapperAt(x, lightBlockingHeight, z, blockNames, previousBlock);
                        result = result * blockPrime + previousBlock.hashCode();
                        result = result * biomePrime + biomeHashAt(x, z, biomeNames, useEmptyBiomes);
                        result = result * yPrime + solidHeight;
                    }
                }
            }

            return result;
        }

        private IBlockStateWrapper blockWrapperAt(
                int x,
                int y,
                int z,
                Map<Integer, String> blockNames,
                IBlockStateWrapper previousBlock) {
            ColumnValue value = columnValue(x, y, z, blockNames);
            if (previousBlock != null && previousBlock.getSerialString().equals(value.blockSerial)) {
                return previousBlock;
            }
            return StaticBlockWrapper.of(value.blockSerial);
        }

        private int biomeHashAt(int x, int z, Map<Integer, String> biomeNames, boolean useEmptyBiomes) {
            if (useEmptyBiomes) {
                return EMPTY_BIOME.hashCode();
            }
            int biomeId = biomeAt(x, z);
            return biomeWrapper(biomeNames.getOrDefault(biomeId, unknownBiomeName(biomeId))).hashCode();
        }

        private int lightBlockingHeightMapValue(int x, int z) {
            int index = z * CHUNK_WIDTH + x;
            if (index < 0 || index >= heightMap.length) {
                return 0;
            }
            return Math.max(0, Math.min(255, heightMap[index]));
        }
    }

    private static final class ChunkSection {
        final int y;
        final byte[] blocks;
        final byte[] blocks16;
        final byte[] add;
        final byte[] data;
        final byte[] data16;
        final byte[] blockLight;
        final byte[] skyLight;

        private ChunkSection(int y, byte[] blocks, byte[] blocks16, byte[] add, byte[] data, byte[] data16, byte[] blockLight, byte[] skyLight) {
            this.y = y;
            this.blocks = blocks == null ? new byte[4096] : blocks;
            this.blocks16 = blocks16;
            this.add = add;
            this.data = data;
            this.data16 = data16;
            this.blockLight = blockLight;
            this.skyLight = skyLight;
        }

        static ChunkSection from(Nbt.Compound section) {
            return new ChunkSection(section.byteValue("Y", (byte) 0) & 0xFF,
                    section.byteArray("Blocks"),
                    section.byteArray("Blocks16"),
                    section.byteArray("Add"),
                    section.byteArray("Data"),
                    section.byteArray("Data16"),
                    section.byteArray("BlockLight"),
                    section.byteArray("SkyLight"));
        }

        int blockId(int index) {
            if (blocks16 != null && index * 2 + 1 < blocks16.length) {
                return unsignedShort16(blocks16, index);
            }
            int id = blocks[index] & 0xFF;
            int addBits = nibble(add, index, 0);
            return id | (addBits << 8);
        }

        int blockMeta(int index) {
            if (data16 != null && index * 2 + 1 < data16.length) {
                return unsignedShort16(data16, index);
            }
            return nibble(data, index, 0);
        }
    }

    private static int unsignedShort16(byte[] bytes, int index) {
        int byteIndex = index * 2;
        return ((bytes[byteIndex] & 0xFF) << 8) | (bytes[byteIndex + 1] & 0xFF);
    }

    private static final class AnvilRegion implements AutoCloseable {
        private final byte[] bytes;
        private final int[] offsets = new int[1024];

        AnvilRegion(Path path) throws IOException {
            this.bytes = Files.readAllBytes(path);
            if (bytes.length < 8192) {
                throw new IOException("Region file is too small: " + path);
            }
            for (int i = 0; i < 1024; i++) {
                int base = i * 4;
                int sector = ((bytes[base] & 0xFF) << 16) | ((bytes[base + 1] & 0xFF) << 8) | (bytes[base + 2] & 0xFF);
                int count = bytes[base + 3] & 0xFF;
                offsets[i] = (sector << 8) | count;
            }
        }

        Nbt.Compound readChunk(int localX, int localZ) throws IOException {
            int index = localX + localZ * CHUNKS_PER_REGION;
            int entry = offsets[index];
            int sector = entry >>> 8;
            int sectorCount = entry & 0xFF;
            if (sector == 0 || sectorCount == 0) {
                return null;
            }
            int byteOffset = sector * 4096;
            if (byteOffset + 5 > bytes.length) {
                throw new EOFException("Chunk sector offset outside region file");
            }
            int length = ((bytes[byteOffset] & 0xFF) << 24)
                    | ((bytes[byteOffset + 1] & 0xFF) << 16)
                    | ((bytes[byteOffset + 2] & 0xFF) << 8)
                    | (bytes[byteOffset + 3] & 0xFF);
            if (length <= 1 || byteOffset + 4 + length > bytes.length) {
                throw new EOFException("Chunk length outside region file");
            }
            int compression = bytes[byteOffset + 4] & 0xFF;
            byte[] compressed = Arrays.copyOfRange(bytes, byteOffset + 5, byteOffset + 4 + length);
            InputStream raw = new ByteArrayInputStream(compressed);
            InputStream nbtIn;
            if (compression == 1) {
                nbtIn = new GZIPInputStream(raw);
            } else if (compression == 2) {
                nbtIn = new InflaterInputStream(raw);
            } else {
                throw new IOException("Unsupported Anvil compression type: " + compression);
            }
            try (nbtIn) {
                Object root = Nbt.readNamed(nbtIn).value;
                if (!(root instanceof Nbt.Compound compound)) {
                    throw new IOException("Chunk root is not a compound");
                }
                return compound;
            }
        }

        @Override
        public void close() {
            // byte array backed, nothing to close
        }
    }

    private static final class StaticLevelWrapper implements ILevelWrapper {
        static final StaticLevelWrapper INSTANCE = new StaticLevelWrapper();
        private IDhLevel dhLevel;

        @Override
        public IDimensionTypeWrapper getDimensionType() {
            return StaticDimensionTypeWrapper.INSTANCE;
        }

        @Override
        public String getDimensionName() {
            return "overworld";
        }

        @Override
        public long getHashedSeed() {
            return 0L;
        }

        @Override
        public String getDhIdentifier() {
            return "0000000000000@overworld";
        }

        @Override
        public EDhApiLevelType getLevelType() {
            return EDhApiLevelType.CLIENT_LEVEL;
        }

        @Override
        public boolean hasCeiling() {
            return false;
        }

        @Override
        public boolean hasSkyLight() {
            return true;
        }

        @Override
        public int getMaxHeight() {
            return WORLD_MAX_Y_EXCLUSIVE;
        }

        @Override
        public int getMinHeight() {
            return WORLD_MIN_Y;
        }

        @Override
        public void onUnload() {
        }

        @Override
        public void setDhLevel(IDhLevel dhLevel) {
            this.dhLevel = dhLevel;
        }

        @Override
        public IDhLevel getDhLevel() {
            return dhLevel;
        }

        @Override
        public IDhApiCustomRenderRegister getRenderRegister() {
            return null;
        }

        @Override
        public File getDhSaveFolder() {
            return new File(".");
        }

        @Override
        public Object getWrappedMcObject() {
            return null;
        }

        @Override
        public void finishDelayedSetup() {
        }
    }

    private static final class StaticDimensionTypeWrapper implements IDimensionTypeWrapper {
        static final StaticDimensionTypeWrapper INSTANCE = new StaticDimensionTypeWrapper();

        @Override
        public boolean hasCeiling() {
            return false;
        }

        @Override
        public String getName() {
            return "overworld";
        }

        @Override
        public boolean hasSkyLight() {
            return true;
        }

        @Override
        public boolean isTheEnd() {
            return false;
        }

        @Override
        public double getCoordinateScale() {
            return 1.0;
        }

        @Override
        public Object getWrappedMcObject() {
            return null;
        }

        @Override
        public void finishDelayedSetup() {
        }
    }

    private static final class StaticWrapperFactory implements IWrapperFactory {
        private static boolean registered;

        static synchronized void register() {
            if (registered) {
                return;
            }
            try {
                SingletonInjector.INSTANCE.bind(IWrapperFactory.class, new StaticWrapperFactory());
            } catch (IllegalStateException ignored) {
                // The Minecraft client may already have registered one. In standalone use this should not happen.
            }
            registered = true;
        }

        @Override
        public IBiomeWrapper getBiomeWrapper(Object[] objects, IDhApiLevelWrapper levelWrapper) {
            if (objects != null && objects.length > 0 && objects[0] instanceof String value) {
                return biomeWrapper(value);
            }
            return PLAINS_BIOME;
        }

        @Override
        public IBlockStateWrapper getBlockStateWrapper(Object[] objects, IDhApiLevelWrapper levelWrapper) {
            if (objects != null && objects.length > 0 && objects[0] instanceof String value) {
                return StaticBlockWrapper.of(value);
            }
            return AIR_BLOCK;
        }

        @Override
        public IBiomeWrapper getBiomeWrapper(String resourceLocationString, IDhApiLevelWrapper levelWrapper) {
            return biomeWrapper(resourceLocationString);
        }

        @Override
        public IBlockStateWrapper getDefaultBlockStateWrapper(String resourceLocationString, IDhApiLevelWrapper levelWrapper) {
            return StaticBlockWrapper.of(resourceLocationString);
        }

        @Override
        public IBatchGeneratorEnvironmentWrapper createBatchGenerator(IDhLevel level) {
            return null;
        }

        @Override
        public IBiomeWrapper deserializeBiomeWrapper(String resourceLocationString, ILevelWrapper levelWrapper) {
            return biomeWrapper(resourceLocationString);
        }

        @Override
        public IBiomeWrapper getPlainsBiomeWrapper(ILevelWrapper levelWrapper) {
            return PLAINS_BIOME;
        }

        @Override
        public IBlockStateWrapper deserializeBlockStateWrapper(String resourceLocationString, ILevelWrapper levelWrapper) {
            return StaticBlockWrapper.of(resourceLocationString);
        }

        @Override
        public IBlockStateWrapper getAirBlockStateWrapper() {
            return AIR_BLOCK;
        }

        @Override
        public IBlockStateWrapper getWaterBlockStateWrapper(ILevelWrapper levelWrapper) {
            return StaticBlockWrapper.of("minecraft:water");
        }

        @Override
        public ObjectOpenHashSet<IBlockStateWrapper> getRendererIgnoredBlocks(ILevelWrapper levelWrapper) {
            return new ObjectOpenHashSet<>();
        }

        @Override
        public ObjectOpenHashSet<IBlockStateWrapper> getRendererIgnoredCaveBlocks(ILevelWrapper levelWrapper) {
            return new ObjectOpenHashSet<>();
        }

        @Override
        public ObjectOpenHashSet<IBlockStateWrapper> getWaterSubsurfaceReplacementBlocks(ILevelWrapper levelWrapper) {
            return new ObjectOpenHashSet<>();
        }

        @Override
        public ObjectOpenHashSet<IBlockStateWrapper> getWaterSurfaceReplacementBlocks(ILevelWrapper levelWrapper) {
            return new ObjectOpenHashSet<>();
        }

        @Override
        public void resetCachedIgnoredBlocksSets() {
        }

        @Override
        public IChunkWrapper createChunkWrapper(Object[] objects) {
            return null;
        }

        @Override
        public IVertexBufferWrapper createVboWrapper(String name) {
            return null;
        }

        @Override
        public ILodContainerUniformBufferWrapper createLodContainerUniformWrapper() {
            return null;
        }

        @Override
        public IDhGenericObjectVertexBufferContainer createGenericObjectVboContainer() {
            return null;
        }

        @Override
        public IDhGenericRenderer createGenericRenderer() {
            return null;
        }
    }

    private static final class StaticBiomeWrapper implements IBiomeWrapper {
        private static final Map<String, StaticBiomeWrapper> CACHE = new HashMap<>();
        private final String serialString;
        private final String name;

        private StaticBiomeWrapper(String serialString) {
            this.serialString = serialString == null || serialString.isBlank() ? "EMPTY" : serialString;
            this.name = this.serialString.startsWith("biome:")
                    ? this.serialString.substring("biome:".length())
                    : this.serialString;
        }

        static synchronized StaticBiomeWrapper of(String serialString) {
            return CACHE.computeIfAbsent(serialString == null ? "EMPTY" : serialString, StaticBiomeWrapper::new);
        }

        @Override
        public String getName() {
            return name;
        }

        @Override
        public String getSerialString() {
            return serialString;
        }

        @Override
        public Object getWrappedMcObject() {
            return null;
        }

        @Override
        public boolean equals(Object other) {
            return other instanceof IBiomeWrapper wrapper && serialString.equals(wrapper.getSerialString());
        }

        @Override
        public int hashCode() {
            return Objects.hash(serialString);
        }
    }

    private static final class StaticBlockWrapper implements IBlockStateWrapper {
        private static final Map<String, StaticBlockWrapper> CACHE = new HashMap<>();
        private final String serialString;
        private final String baseName;
        private final boolean air;
        private final boolean liquid;
        private final boolean solid;
        private final Color mapColor;
        private final EDhApiBlockMaterial material;

        private StaticBlockWrapper(String serialString) {
            this.serialString = serialString == null || serialString.isBlank() ? "AIR" : serialString;
            this.baseName = baseBlockName(this.serialString).toLowerCase(Locale.ROOT);
            this.air = "AIR".equals(this.serialString) || "minecraft:air".equals(this.baseName);
            this.liquid = baseName.contains("water") || baseName.contains("lava");
            this.solid = !air && !liquid && !baseName.contains("torch") && !baseName.contains("flower")
                    && !baseName.contains("grass") && !baseName.contains("sapling") && !baseName.contains("vine")
                    && !baseName.contains("mushroom") && !baseName.contains("reeds") && !baseName.contains("fire");
            this.material = inferMaterial(baseName, air, liquid);
            this.mapColor = inferColor(baseName, material);
        }

        static synchronized StaticBlockWrapper of(String serialString) {
            return CACHE.computeIfAbsent(serialString == null ? "AIR" : serialString, StaticBlockWrapper::new);
        }

        private static String baseBlockName(String serialString) {
            if ("AIR".equals(serialString)) {
                return serialString;
            }
            int firstColon = serialString.indexOf(':');
            if (firstColon < 0) {
                return serialString;
            }
            int secondColon = serialString.indexOf(':', firstColon + 1);
            return secondColon < 0 ? serialString : serialString.substring(0, secondColon);
        }

        @Override
        public boolean isAir() {
            return air;
        }

        @Override
        public boolean isSolid() {
            return solid;
        }

        @Override
        public boolean isLiquid() {
            return liquid;
        }

        @Override
        public String getSerialString() {
            return serialString;
        }

        @Override
        public int getOpacity() {
            if (air || liquid || baseName.contains("glass") || baseName.contains("leaves") || baseName.contains("torch")
                    || baseName.contains("flower") || baseName.contains("grass") || baseName.contains("vine")) {
                return 0;
            }
            return 15;
        }

        @Override
        public int getLightEmission() {
            if (baseName.contains("lava") || baseName.contains("glowstone") || baseName.contains("torch")
                    || baseName.contains("fire") || baseName.contains("lit_") || baseName.contains("lamp")) {
                return 15;
            }
            return 0;
        }

        @Override
        public byte getMaterialId() {
            return material.index;
        }

        @Override
        public boolean isBeaconBlock() {
            return baseName.contains("beacon");
        }

        @Override
        public boolean isBeaconTintBlock() {
            return baseName.contains("glass");
        }

        @Override
        public boolean allowsBeaconBeamPassage() {
            return air || baseName.contains("glass");
        }

        @Override
        public boolean isBeaconBaseBlock() {
            return baseName.contains("iron_block") || baseName.contains("gold_block")
                    || baseName.contains("diamond_block") || baseName.contains("emerald_block");
        }

        @Override
        public Color getMapColor() {
            return mapColor;
        }

        @Override
        public Color getBeaconTintColor() {
            return mapColor;
        }

        @Override
        public Object getWrappedMcObject() {
            return null;
        }

        @Override
        public boolean equals(Object other) {
            return other instanceof IBlockStateWrapper wrapper && serialString.equals(wrapper.getSerialString());
        }

        @Override
        public int hashCode() {
            return Objects.hash(serialString);
        }

        private static EDhApiBlockMaterial inferMaterial(String name, boolean air, boolean liquid) {
            if (air) return EDhApiBlockMaterial.AIR;
            if (name.contains("water")) return EDhApiBlockMaterial.WATER;
            if (name.contains("lava")) return EDhApiBlockMaterial.LAVA;
            if (name.contains("leaf") || name.contains("leaves")) return EDhApiBlockMaterial.LEAVES;
            if (name.contains("grass")) return EDhApiBlockMaterial.GRASS;
            if (name.contains("dirt") || name.contains("clay") || name.contains("farmland")) return EDhApiBlockMaterial.DIRT;
            if (name.contains("sand")) return EDhApiBlockMaterial.SAND;
            if (name.contains("snow") || name.contains("ice")) return EDhApiBlockMaterial.SNOW;
            if (name.contains("log") || name.contains("wood") || name.contains("plank")) return EDhApiBlockMaterial.WOOD;
            if (name.contains("iron") || name.contains("gold") || name.contains("copper") || name.contains("steel")) {
                return EDhApiBlockMaterial.METAL;
            }
            if (name.contains("netherrack") || name.contains("nether")) return EDhApiBlockMaterial.NETHER_STONE;
            if (name.contains("glowstone") || name.contains("torch") || name.contains("lamp")) return EDhApiBlockMaterial.ILLUMINATED;
            if (liquid) return EDhApiBlockMaterial.UNKNOWN;
            return EDhApiBlockMaterial.STONE;
        }

        private static Color inferColor(String name, EDhApiBlockMaterial material) {
            return switch (material) {
                case AIR -> new Color(0, 0, 0, 0);
                case WATER -> new Color(48, 84, 160);
                case LAVA, ILLUMINATED -> new Color(230, 110, 35);
                case LEAVES, GRASS -> new Color(90, 140, 70);
                case DIRT -> new Color(115, 82, 52);
                case SAND -> new Color(210, 195, 135);
                case SNOW -> new Color(235, 240, 240);
                case WOOD -> new Color(125, 86, 48);
                case METAL -> new Color(165, 165, 160);
                case NETHER_STONE -> new Color(90, 38, 38);
                default -> {
                    if (name.contains("ore")) {
                        yield new Color(120, 120, 120);
                    }
                    yield new Color(105, 105, 105);
                }
            };
        }
    }

    private static final class Args {
        Path save;
        Path out;
        Path validationWorld;
        Path validateDbOnly;
        Path scanDbMappings;
        Path inspectDbPos;
        Path inspectSaveChunk;
        Path buildParentLodsOnly;
        Path rebuildChunkHashesOnly;
        int inspectDetailLevel;
        int inspectPosX;
        int inspectPosZ;
        int inspectChunkX;
        int inspectChunkZ;
        boolean overwrite;
        boolean buildParentLods = true;
        boolean centerChunkSet;
        int centerChunkX;
        int centerChunkZ;
        int radiusChunks = 32;
        int maxRegionFiles = 0;
        long maxChunks = 0;
        int progressEveryRegions = 10;
        EDhApiDataCompressionMode compressionMode = EDhApiDataCompressionMode.Z_STD_BLOCK;

        static Args parse(String[] rawArgs) {
            Args args = new Args();
            for (int i = 0; i < rawArgs.length; i++) {
                String arg = rawArgs[i];
                switch (arg) {
                    case "--save" -> args.save = Path.of(requireValue(rawArgs, ++i, arg));
                    case "--out" -> args.out = Path.of(requireValue(rawArgs, ++i, arg));
                    case "--validation-world" -> args.validationWorld = Path.of(requireValue(rawArgs, ++i, arg));
                    case "--validate-db" -> args.validateDbOnly = Path.of(requireValue(rawArgs, ++i, arg));
                    case "--scan-db-mappings" -> args.scanDbMappings = Path.of(requireValue(rawArgs, ++i, arg));
                    case "--inspect-db-pos" -> {
                        args.inspectDbPos = Path.of(requireValue(rawArgs, ++i, arg));
                        String[] parts = requireValue(rawArgs, ++i, arg).split(",");
                        if (parts.length != 3) {
                            throw new IllegalArgumentException("--inspect-db-pos requires <sqlite> <dbDetail,posX,posZ>");
                        }
                        args.inspectDetailLevel = Integer.parseInt(parts[0].trim());
                        args.inspectPosX = Integer.parseInt(parts[1].trim());
                        args.inspectPosZ = Integer.parseInt(parts[2].trim());
                    }
                    case "--inspect-save-chunk" -> {
                        args.inspectSaveChunk = Path.of(requireValue(rawArgs, ++i, arg));
                        String[] parts = requireValue(rawArgs, ++i, arg).split(",");
                        if (parts.length != 2) {
                            throw new IllegalArgumentException("--inspect-save-chunk requires <save> <chunkX,chunkZ>");
                        }
                        args.inspectChunkX = Integer.parseInt(parts[0].trim());
                        args.inspectChunkZ = Integer.parseInt(parts[1].trim());
                    }
                    case "--build-parent-lods-only" -> args.buildParentLodsOnly = Path.of(requireValue(rawArgs, ++i, arg));
                    case "--rebuild-chunk-hashes-only" -> args.rebuildChunkHashesOnly = Path.of(requireValue(rawArgs, ++i, arg));
                    case "--overwrite" -> args.overwrite = true;
                    case "--no-build-parent-lods" -> args.buildParentLods = false;
                    case "--radius-chunks" -> args.radiusChunks = Integer.parseInt(requireValue(rawArgs, ++i, arg));
                    case "--max-region-files" -> args.maxRegionFiles = Integer.parseInt(requireValue(rawArgs, ++i, arg));
                    case "--max-chunks" -> args.maxChunks = Long.parseLong(requireValue(rawArgs, ++i, arg));
                    case "--compression" -> args.compressionMode = EDhApiDataCompressionMode.valueOf(requireValue(rawArgs, ++i, arg));
                    case "--progress-every-regions" -> args.progressEveryRegions = Integer.parseInt(requireValue(rawArgs, ++i, arg));
                    case "--center-chunk" -> {
                        String[] parts = requireValue(rawArgs, ++i, arg).split(",");
                        if (parts.length != 2) {
                            throw new IllegalArgumentException("--center-chunk must be X,Z");
                        }
                        args.centerChunkX = Integer.parseInt(parts[0].trim());
                        args.centerChunkZ = Integer.parseInt(parts[1].trim());
                        args.centerChunkSet = true;
                    }
                    default -> throw new IllegalArgumentException("Unknown argument: " + arg);
                }
            }
            if (args.radiusChunks < 0) {
                throw new IllegalArgumentException("--radius-chunks must be non-negative");
            }
            return args;
        }

        private static String requireValue(String[] args, int index, String flag) {
            if (index >= args.length) {
                throw new IllegalArgumentException("Missing value for " + flag);
            }
            return args[index];
        }
    }

    private static final class Nbt {
        record NamedTag(String name, Object value) {
        }

        static final class Compound extends LinkedHashMap<String, Object> {
            Compound compound(String key) {
                Object value = get(key);
                return value instanceof Compound compound ? compound : new Compound();
            }

            ListTag list(String key) {
                Object value = get(key);
                return value instanceof ListTag list ? list : null;
            }

            String string(String key, String fallback) {
                Object value = get(key);
                return value instanceof String string ? string : fallback;
            }

            int intValue(String key, int fallback) {
                Object value = get(key);
                return value instanceof Number number ? number.intValue() : fallback;
            }

            long longValue(String key, long fallback) {
                Object value = get(key);
                return value instanceof Number number ? number.longValue() : fallback;
            }

            byte byteValue(String key, byte fallback) {
                Object value = get(key);
                return value instanceof Number number ? number.byteValue() : fallback;
            }

        byte[] byteArray(String key) {
            Object value = get(key);
            return value instanceof byte[] bytes ? bytes : null;
        }

        int[] intArray(String key) {
            Object value = get(key);
            return value instanceof int[] ints ? ints : null;
        }
    }

        record ListTag(byte elementType, List<Object> values) {
        }

        static NamedTag readNamed(InputStream raw) throws IOException {
            DataInputStream in = new DataInputStream(new BufferedInputStream(raw));
            byte type = in.readByte();
            if (type == 0) {
                return new NamedTag("", null);
            }
            String name = readString(in);
            return new NamedTag(name, readPayload(in, type));
        }

        private static Object readPayload(DataInputStream in, byte type) throws IOException {
            return switch (type) {
                case 1 -> in.readByte();
                case 2 -> in.readShort();
                case 3 -> in.readInt();
                case 4 -> in.readLong();
                case 5 -> in.readFloat();
                case 6 -> in.readDouble();
                case 7 -> {
                    int length = in.readInt();
                    byte[] bytes = new byte[length];
                    in.readFully(bytes);
                    yield bytes;
                }
                case 8 -> readString(in);
                case 9 -> readList(in);
                case 10 -> readCompound(in);
                case 11 -> {
                    int length = in.readInt();
                    int[] ints = new int[length];
                    for (int i = 0; i < length; i++) {
                        ints[i] = in.readInt();
                    }
                    yield ints;
                }
                case 12 -> {
                    int length = in.readInt();
                    long[] longs = new long[length];
                    for (int i = 0; i < length; i++) {
                        longs[i] = in.readLong();
                    }
                    yield longs;
                }
                default -> throw new IOException("Unsupported NBT tag type: " + type);
            };
        }

        private static Compound readCompound(DataInputStream in) throws IOException {
            Compound compound = new Compound();
            while (true) {
                byte type = in.readByte();
                if (type == 0) {
                    return compound;
                }
                String name = readString(in);
                compound.put(name, readPayload(in, type));
            }
        }

        private static ListTag readList(DataInputStream in) throws IOException {
            byte elementType = in.readByte();
            int length = in.readInt();
            List<Object> values = new ArrayList<>(Math.max(length, 0));
            for (int i = 0; i < length; i++) {
                values.add(readPayload(in, elementType));
            }
            return new ListTag(elementType, values);
        }

        private static String readString(DataInputStream in) throws IOException {
            int length = in.readUnsignedShort();
            byte[] bytes = new byte[length];
            in.readFully(bytes);
            return new String(bytes, StandardCharsets.UTF_8);
        }
    }
}
