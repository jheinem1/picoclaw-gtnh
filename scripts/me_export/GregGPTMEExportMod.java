package greggpt.meexport;

import cpw.mods.fml.common.FMLCommonHandler;
import cpw.mods.fml.common.Mod;
import cpw.mods.fml.common.eventhandler.SubscribeEvent;
import cpw.mods.fml.common.gameevent.TickEvent;
import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStreamWriter;
import java.io.PrintWriter;
import java.io.StringWriter;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Date;
import java.util.HashMap;
import java.util.IdentityHashMap;
import java.util.Iterator;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TimeZone;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ThreadFactory;

@Mod(
    modid = "greggpt_me_export",
    name = "GregGPT ME Export",
    version = "1.0.0",
    acceptableRemoteVersions = "*"
)
public final class GregGPTMEExportMod {
  private static final long DEFAULT_INTERVAL_SECONDS = 300L;
  private static final Object CACHE_MISS = new Object();
  private static final Map<String, Object> CLASS_CACHE = new HashMap<String, Object>();
  private static final Map<String, Object> METHOD_CACHE = new HashMap<String, Object>();
  private static final Map<String, Object> FIELD_CACHE = new HashMap<String, Object>();
  private static final ExecutorService WRITER =
      Executors.newSingleThreadExecutor(
          new ThreadFactory() {
            public Thread newThread(Runnable r) {
              Thread t = new Thread(r, "GregGPT ME Export Writer");
              t.setDaemon(true);
              return t;
            }
          });

  private static long nextMETick = 0L;
  private static long nextBlockInventoryTick = 0L;
  private static long tickCounter = 0L;
  private static MEExportJob meExportJob = null;
  private static BlockInventoryJob blockInventoryJob = null;

  public GregGPTMEExportMod() {
    FMLCommonHandler.instance().bus().register(this);
  }

  @SubscribeEvent
  public void onServerTick(TickEvent.ServerTickEvent event) {
    if (event.phase != TickEvent.Phase.END) {
      return;
    }
    tickCounter++;
    Object server = null;

    if (!boolProperty("greggpt.me_export_enabled", true)) {
      meExportJob = null;
    } else {
      try {
        if (meExportJob == null && tickCounter >= nextMETick) {
          nextMETick = tickCounter + intervalTicks("greggpt.me_export_interval_seconds", DEFAULT_INTERVAL_SECONDS);
          server = serverInstance();
          meExportJob = startMEExportJob(server);
        }
        if (meExportJob != null && meExportJob.processTick()) {
          meExportJob.finish();
          meExportJob = null;
        }
      } catch (Throwable t) {
        meExportJob = null;
        System.out.println("[GREGGPT-ME] export failed: " + t);
        t.printStackTrace(System.out);
      }
    }

    if (!boolProperty("greggpt.block_inventory_export_enabled", true)) {
      blockInventoryJob = null;
      return;
    }

    try {
      if (blockInventoryJob == null && tickCounter >= nextBlockInventoryTick) {
        nextBlockInventoryTick =
            tickCounter + intervalTicks("greggpt.block_inventory_export_interval_seconds", DEFAULT_INTERVAL_SECONDS);
        if (server == null) {
          server = serverInstance();
        }
        blockInventoryJob = startBlockInventoryJob(server);
      }
      if (blockInventoryJob != null && blockInventoryJob.processTick()) {
        blockInventoryJob.finish();
        blockInventoryJob = null;
      }
    } catch (Throwable t) {
      blockInventoryJob = null;
      System.out.println("[PICOCLAW-BLOCKINV] export failed: " + t);
      t.printStackTrace(System.out);
    }
  }

  private static Object serverInstance() throws Exception {
    return invokeAny(
        FMLCommonHandler.instance(),
        new String[] {"getMinecraftServerInstance"},
        new Class[0],
        new Object[0]);
  }

  private static boolean boolProperty(String name, boolean defaultValue) {
    String raw = System.getProperty(name);
    if (raw == null) {
      return defaultValue;
    }
    return Boolean.parseBoolean(raw);
  }

  private static long intervalTicks(String propertyName, long defaultSeconds) {
    String raw = System.getProperty(propertyName, String.valueOf(defaultSeconds));
    try {
      long seconds = Long.parseLong(raw);
      if (seconds < 1L) {
        seconds = 1L;
      }
      return seconds * 20L;
    } catch (NumberFormatException ignored) {
      return defaultSeconds * 20L;
    }
  }

  private static int intProperty(String propertyName, int defaultValue, int minValue) {
    String raw = System.getProperty(propertyName, String.valueOf(defaultValue));
    try {
      int value = Integer.parseInt(raw);
      return value < minValue ? minValue : value;
    } catch (NumberFormatException ignored) {
      return defaultValue;
    }
  }

  private static void writeMEDump(Object server) throws Exception {
    if (server == null) {
      return;
    }
    File out = outputFile(server);
    String json = buildMEJson(server);
    submitWrite(out, json, "[GREGGPT-ME]", "wrote " + out.getAbsolutePath());
  }

  private static MEExportJob startMEExportJob(Object server) throws Exception {
    if (server == null) {
      return null;
    }
    File out = outputFile(server);
    List<Object> tiles = new ArrayList<Object>();
    Object[] worlds = (Object[]) getField(server, "worldServers", "field_71305_c");
    for (Object world : worlds) {
      if (world == null) {
        continue;
      }
      Object loaded = getField(world, "loadedTileEntityList", "field_147482_g");
      if (!(loaded instanceof Iterable)) {
        continue;
      }
      for (Object te : (Iterable<?>) loaded) {
        tiles.add(te);
      }
    }
    MEExportJob job = new MEExportJob(out, tiles, nowUTC());
    System.out.println("[GREGGPT-ME] started async snapshot tiles=" + tiles.size());
    return job;
  }

  private static final class MEExportJob {
    private final File out;
    private final List<Object> tiles;
    private final String generatedAt;
    private final int tilesPerTick;
    private final int itemsPerTick;
    private final long budgetNanos;
    private final IdentityHashMap<Object, Boolean> seenGrids = new IdentityHashMap<Object, Boolean>();
    private final List<String> networks = new ArrayList<String>();
    private int tileIndex = 0;
    private int totalEntries = 0;
    private int totalPositive = 0;
    private long startedNanos = System.nanoTime();
    private MENetworkSnapshot current = null;

    MEExportJob(File out, List<Object> tiles, String generatedAt) {
      this.out = out;
      this.tiles = tiles;
      this.generatedAt = generatedAt;
      this.tilesPerTick = intProperty("greggpt.me_export_tiles_per_tick", 64, 1);
      this.itemsPerTick = intProperty("greggpt.me_export_items_per_tick", 128, 1);
      this.budgetNanos = (long) intProperty("greggpt.me_export_budget_ms", 2, 1) * 1000000L;
    }

    boolean processTick() {
      long started = System.nanoTime();
      int tilesProcessed = 0;
      int itemsProcessed = 0;
      while (true) {
        if (current != null) {
          int before = current.entryCount;
          boolean done = current.process(itemsPerTick - itemsProcessed);
          itemsProcessed += current.entryCount - before;
          if (done) {
            networks.add(current.finish());
            totalEntries += current.entryCount;
            totalPositive += current.positiveCount;
            current = null;
          }
        }

        if (current != null) {
          return false;
        }
        if (tileIndex >= tiles.size()) {
          return true;
        }
        if (tilesProcessed >= tilesPerTick || itemsProcessed >= itemsPerTick) {
          return false;
        }
        if ((tilesProcessed > 0 || itemsProcessed > 0) && System.nanoTime() - started >= budgetNanos) {
          return false;
        }

        Object te = tiles.get(tileIndex++);
        tilesProcessed++;
        Object grid = findGrid(te);
        if (grid == null || seenGrids.containsKey(grid)) {
          continue;
        }
        seenGrids.put(grid, Boolean.TRUE);
        Iterable<?> items = storageList(grid);
        if (items == null) {
          continue;
        }
        current = new MENetworkSnapshot(grid, te, items);
      }
    }

    void finish() {
      StringBuilder json = new StringBuilder(networks.size() * 512 + 64);
      json.append("{\"generated_at\":\"");
      json.append(escape(generatedAt));
      json.append("\",\"networks\":[");
      for (int i = 0; i < networks.size(); i++) {
        if (i > 0) {
          json.append(',');
        }
        json.append(networks.get(i));
      }
      json.append("]}");
      long elapsedMs = (System.nanoTime() - startedNanos) / 1000000L;
      submitWrite(
          out,
          json.toString(),
          "[GREGGPT-ME]",
          "wrote " + out.getAbsolutePath() + " networks=" + networks.size() + " entries=" + totalEntries
              + " positive=" + totalPositive + " elapsed_ms=" + elapsedMs);
    }
  }

  private static final class MENetworkSnapshot {
    private final Object grid;
    private final Iterator<?> items;
    private final String networkID;
    private final String label;
    private final int dim;
    private final int x;
    private final int y;
    private final int z;
    private final StringBuilder itemJson = new StringBuilder();
    private int entryCount = 0;
    private int convertedCount = 0;
    private int positiveCount = 0;
    private String firstEntryClass = "";
    private String firstStackClass = "";
    private boolean first = true;

    MENetworkSnapshot(Object grid, Object te, Iterable<?> items) {
      this.grid = grid;
      this.items = items.iterator();
      this.x = intField(te, "xCoord", "field_145851_c");
      this.y = intField(te, "yCoord", "field_145848_d");
      this.z = intField(te, "zCoord", "field_145849_e");
      this.dim = dimensionID(te);
      this.networkID = Integer.toHexString(System.identityHashCode(grid));
      this.label = "ME network " + x + "," + y + "," + z;
    }

    boolean process(int maxItems) {
      if (maxItems < 1) {
        maxItems = 1;
      }
      int processed = 0;
      while (items.hasNext() && processed < maxItems) {
        Object entry = items.next();
        processed++;
        entryCount++;
        if (firstEntryClass.length() == 0 && entry != null) {
          firstEntryClass = entry.getClass().getName();
        }
        Object stack = toItemStack(entry);
        if (stack != null) {
          convertedCount++;
          if (firstStackClass.length() == 0) {
            firstStackClass = stack.getClass().getName();
          }
        }
        Object item = stack != null ? invokeQuiet(stack, new String[] {"getItem", "func_77973_b"}, new Class[0], new Object[0]) : null;
        int count = stack != null ? intField(stack, "stackSize", "field_77994_a") : 0;
        if (stack == null || item == null || count <= 0) {
          continue;
        }
        positiveCount++;
        if (!first) {
          itemJson.append(',');
        }
        first = false;
        itemJson.append(itemJson(stack));
      }
      return !items.hasNext();
    }

    String finish() {
      StringBuilder out = new StringBuilder(itemJson.length() + 256);
      out.append("{\"network_id\":\"");
      out.append(escape(networkID));
      out.append("\",\"label\":\"");
      out.append(escape(label));
      out.append("\",\"dim\":");
      out.append(dim);
      out.append(",\"x\":");
      out.append(x);
      out.append(",\"y\":");
      out.append(y);
      out.append(",\"z\":");
      out.append(z);
      out.append(",\"entry_count\":");
      out.append(entryCount);
      out.append(",\"converted_count\":");
      out.append(convertedCount);
      out.append(",\"positive_count\":");
      out.append(positiveCount);
      out.append(",\"first_entry_class\":\"");
      out.append(escape(firstEntryClass));
      out.append("\",\"first_stack_class\":\"");
      out.append(escape(firstStackClass));
      out.append("\",\"items\":[");
      out.append(itemJson.toString());
      out.append("]}");
      return out.toString();
    }
  }

  private static String buildMEJson(Object server) throws Exception {
    StringWriter out = new StringWriter();
    PrintWriter w = new PrintWriter(out);
    w.print("{\"generated_at\":\"");
    w.print(escape(nowUTC()));
    w.print("\",\"networks\":[");
    boolean firstNetwork = true;
    IdentityHashMap<Object, Boolean> seenGrids = new IdentityHashMap<Object, Boolean>();
    Object[] worlds = (Object[]) getField(server, "worldServers", "field_71305_c");
    for (Object world : worlds) {
      if (world == null) {
        continue;
      }
      Object loaded = getField(world, "loadedTileEntityList", "field_147482_g");
      if (!(loaded instanceof Iterable)) {
        continue;
      }
      for (Object te : (Iterable<?>) loaded) {
        Object grid = findGrid(te);
        if (grid == null || seenGrids.containsKey(grid)) {
          continue;
        }
        seenGrids.put(grid, Boolean.TRUE);
        Iterable<?> items = storageList(grid);
        if (items == null) {
          continue;
        }
        if (!firstNetwork) {
          w.print(',');
        }
        firstNetwork = false;
        writeNetwork(w, grid, te, items);
      }
    }
    w.print("]}");
    w.flush();
    return out.toString();
  }

  private static void submitWrite(final File out, final String json, final String logPrefix, final String successMessage) {
    WRITER.submit(
        new Runnable() {
          public void run() {
            try {
              writeJsonFile(out, json);
              System.out.println(logPrefix + " " + successMessage);
            } catch (Throwable t) {
              System.out.println(logPrefix + " write failed: " + t);
              t.printStackTrace(System.out);
            }
          }
        });
  }

  private static void writeJsonFile(File out, String json) throws Exception {
    File tmp = new File(out.getParentFile(), out.getName() + ".tmp");
    if (!out.getParentFile().exists() && !out.getParentFile().mkdirs()) {
      throw new IllegalStateException("failed to create " + out.getParentFile());
    }

    PrintWriter w =
        new PrintWriter(new OutputStreamWriter(new FileOutputStream(tmp), StandardCharsets.UTF_8));
    try {
      w.print(json);
    } finally {
      w.close();
    }
    if (!tmp.renameTo(out)) {
      throw new IllegalStateException("failed to rename " + tmp + " to " + out);
    }
  }

  private static BlockInventoryJob startBlockInventoryJob(Object server) throws Exception {
    if (server == null) {
      return null;
    }
    File out = blockInventoryOutputFile(server);
    List<Object> tiles = new ArrayList<Object>();
    Object[] worlds = (Object[]) getField(server, "worldServers", "field_71305_c");
    for (Object world : worlds) {
      if (world == null) {
        continue;
      }
      Object loaded = getField(world, "loadedTileEntityList", "field_147482_g");
      if (!(loaded instanceof Iterable)) {
        continue;
      }
      for (Object te : (Iterable<?>) loaded) {
        tiles.add(te);
      }
    }
    return new BlockInventoryJob(out, tiles, nowUTC());
  }

  private static final class BlockInventoryJob {
    private final File out;
    private final List<Object> tiles;
    private final String generatedAt;
    private final int tilesPerTick;
    private final long budgetNanos;
    private final List<String> rows = new ArrayList<String>();
    private int index = 0;
    private int recordCount = 0;
    private int itemCount = 0;

    BlockInventoryJob(File out, List<Object> tiles, String generatedAt) {
      this.out = out;
      this.tiles = tiles;
      this.generatedAt = generatedAt;
      this.tilesPerTick = intProperty("greggpt.block_inventory_tiles_per_tick", 2, 1);
      this.budgetNanos = (long) intProperty("greggpt.block_inventory_budget_ms", 2, 1) * 1000000L;
    }

    boolean processTick() {
      long started = System.nanoTime();
      int processed = 0;
      while (index < tiles.size() && processed < tilesPerTick) {
        if (processed > 0 && System.nanoTime() - started >= budgetNanos) {
          break;
        }
        Object te = tiles.get(index++);
        processed++;
        try {
          List<String> items = collectBlockInventoryItems(te);
          if (items.isEmpty() && !isInventoryTile(te)) {
            continue;
          }
          rows.add(blockInventoryRecordJson(te, items));
          recordCount++;
          itemCount += items.size();
        } catch (Throwable t) {
          System.out.println("[PICOCLAW-BLOCKINV] tile export failed: " + t);
          t.printStackTrace(System.out);
        }
      }
      return index >= tiles.size();
    }

    void finish() {
      StringBuilder json = new StringBuilder(rows.size() * 256 + 64);
      json.append("{\"generated_at\":\"");
      json.append(escape(generatedAt));
      json.append("\",\"inventories\":[");
      for (int i = 0; i < rows.size(); i++) {
        if (i > 0) {
          json.append(',');
        }
        json.append(rows.get(i));
      }
      json.append("]}");
      submitWrite(
          out,
          json.toString(),
          "[PICOCLAW-BLOCKINV]",
          "wrote " + out.getAbsolutePath() + " count=" + recordCount + " items=" + itemCount);
    }
  }

  private static File outputFile(Object server) throws Exception {
    String folder = String.valueOf(invokeAny(server, new String[] {"getFolderName", "func_71270_I"}, new Class[0], new Object[0]));
    File worldDir = (File) invokeAny(server, new String[] {"getFile", "func_71209_f"}, new Class[] {String.class}, new Object[] {folder});
    return new File(new File(worldDir, "greggpt"), "me_index.json");
  }

  private static File blockInventoryOutputFile(Object server) throws Exception {
    String folder = String.valueOf(invokeAny(server, new String[] {"getFolderName", "func_71270_I"}, new Class[0], new Object[0]));
    File worldDir = (File) invokeAny(server, new String[] {"getFile", "func_71209_f"}, new Class[] {String.class}, new Object[] {folder});
    return new File(new File(worldDir, "picoclaw"), "block_inventories.json");
  }

  private static void writeBlockInventoryRecord(PrintWriter w, Object te, List<String> itemRows) {
    int x = intField(te, "xCoord", "field_145851_c");
    int y = intField(te, "yCoord", "field_145848_d");
    int z = intField(te, "zCoord", "field_145849_e");
    int dim = dimensionID(te);
    Object world = invokeQuiet(te, new String[] {"getWorldObj", "func_145831_w"}, new Class[0], new Object[0]);
    Object block = blockAt(world, x, y, z);
    int meta = blockMetaAt(world, x, y, z);
    Object uid = uniqueIdentifierForBlock(block);
    String reg = "";
    if (uid != null) {
      reg = String.valueOf(getFieldQuiet(uid, "modId")) + ":" + String.valueOf(getFieldQuiet(uid, "name"));
    }
    Object gtMeta = gregTechMetaTileEntity(te);
    int gtMetaID = gregTechMetaID(gtMeta);
    String gtMetaName = gregTechMetaName(gtMeta);
    String displayName = firstNonEmptyString(gtMetaName, blockDisplayName(block));
    String source = inventorySource(te);

    w.print("{\"dim\":");
    w.print(dim);
    w.print(",\"x\":");
    w.print(x);
    w.print(",\"y\":");
    w.print(y);
    w.print(",\"z\":");
    w.print(z);
    w.print(",\"tile_class\":\"");
    w.print(escape(te.getClass().getName()));
    w.print("\",\"tile_id\":\"");
    w.print(escape(firstNonEmptyString(getFieldQuiet(te, "id"), invokeQuiet(te, new String[] {"getClassName"}, new Class[0], new Object[0]))));
    w.print("\",\"block_id\":");
    w.print(blockID(block));
    w.print(",\"block_meta\":");
    w.print(meta);
    if (gtMetaID > 0) {
      w.print(",\"gt_meta_id\":");
      w.print(gtMetaID);
    }
    if (gtMetaName.length() > 0) {
      w.print(",\"gt_meta_name\":\"");
      w.print(escape(gtMetaName));
      w.print("\"");
    }
    w.print(",\"block_reg_name\":\"");
    w.print(escape(reg));
    w.print("\",\"block_display_name\":\"");
    w.print(escape(displayName));
    w.print("\",\"source\":\"");
    w.print(escape(source));
    w.print("\",\"items\":[");
    for (int i = 0; i < itemRows.size(); i++) {
      if (i > 0) {
        w.print(',');
      }
      w.print(itemRows.get(i));
    }
    w.print("]}");
  }

  private static String blockInventoryRecordJson(Object te, List<String> itemRows) {
    StringWriter out = new StringWriter();
    PrintWriter w = new PrintWriter(out);
    writeBlockInventoryRecord(w, te, itemRows);
    w.flush();
    return out.toString();
  }

  private static List<String> collectBlockInventoryItems(Object te) {
    List<String> out = new ArrayList<String>();
    Set<String> seen = new LinkedHashSet<String>();

    try {
      Class<?> iinv = classForName("net.minecraft.inventory.IInventory");
      if (iinv.isInstance(te)) {
        int size = intInvoke(te, "getSizeInventory", "func_70302_i_");
        for (int slot = 0; slot < size; slot++) {
          Object stack = invokeQuiet(te, new String[] {"getStackInSlot", "func_70301_a"}, new Class[] {int.class}, new Object[] {Integer.valueOf(slot)});
          addStackJson(out, seen, stack, slot, inventorySource(te));
        }
      }
    } catch (Throwable ignored) {
    }

    addDirectGregTechStack(out, seen, te);
    collectStackContainer(out, seen, getFieldQuiet(te, "mInventory"), "field:mInventory");
    collectStackContainer(out, seen, getFieldQuiet(te, "mInventoryItems"), "field:mInventoryItems");
    collectStackContainer(out, seen, getFieldQuiet(te, "mInputItems"), "field:mInputItems");
    collectStackContainer(out, seen, getFieldQuiet(te, "mOutputItems"), "field:mOutputItems");
    collectStackContainer(out, seen, getFieldQuiet(te, "Inventory"), "field:Inventory");
    collectStackContainer(out, seen, getFieldQuiet(te, "Items"), "field:Items");
    collectStackContainer(out, seen, getFieldQuiet(te, "inventory"), "field:inventory");
    collectStackContainer(out, seen, getFieldQuiet(te, "contents"), "field:contents");
    return out;
  }

  private static boolean isInventoryTile(Object te) {
    try {
      Class<?> iinv = classForName("net.minecraft.inventory.IInventory");
      if (iinv.isInstance(te)) {
        return true;
      }
    } catch (Throwable ignored) {
    }
    return getFieldQuiet(te, "mInventory") != null
        || getFieldQuiet(te, "mInventoryItems") != null
        || getFieldQuiet(te, "mInputItems") != null
        || getFieldQuiet(te, "mOutputItems") != null
        || getFieldQuiet(te, "Inventory") != null
        || getFieldQuiet(te, "Items") != null
        || getFieldQuiet(te, "inventory") != null
        || getFieldQuiet(te, "contents") != null;
  }

  private static void addDirectGregTechStack(List<String> out, Set<String> seen, Object te) {
    Object stack = firstNonNull(
        getFieldQuiet(te, "mItemStack"),
        getFieldQuiet(te, "mStoredItemStack"),
        getFieldQuiet(te, "mStoredStack"),
        getFieldQuiet(te, "storedItem"));
    if (stack == null) {
      return;
    }
    long count = longField(te, "mItemCount", "mItemCountLong", "mItemAmount", "mStoredItemCount", "mStoredCount");
    if (count <= 0L) {
      count = intField(stack, "stackSize", "field_77994_a");
    }
    Object copy = copyStackWithCount(stack, count);
    addStackJson(out, seen, copy, -1, "gregtech-direct");
  }

  private static void collectStackContainer(List<String> out, Set<String> seen, Object container, String source) {
    if (container == null) {
      return;
    }
    Class<?> c = container.getClass();
    if (c.isArray()) {
      int len = java.lang.reflect.Array.getLength(container);
      for (int i = 0; i < len; i++) {
        addStackJson(out, seen, java.lang.reflect.Array.get(container, i), i, source);
      }
      return;
    }
    if (container instanceof Iterable) {
      int slot = 0;
      for (Object row : (Iterable<?>) container) {
        addStackJson(out, seen, row, slot, source);
        slot++;
      }
      return;
    }
    addStackJson(out, seen, container, -1, source);
  }

  private static void addStackJson(List<String> out, Set<String> seen, Object stack, int slot, String source) {
    if (!isPositiveItemStack(stack)) {
      return;
    }
    String row = itemJson(stack, slot, source);
    if (seen.add(row)) {
      out.add(row);
    }
  }

  private static Object findGrid(Object te) {
    try {
      Object node = null;
      try {
        Class<?> forgeDirection = classForName("net.minecraftforge.common.util.ForgeDirection");
        node = invokeAny(te, new String[] {"getGridNode"}, new Class[] {forgeDirection}, new Object[] {null});
      } catch (Throwable ignored) {
      }
      if (node == null) {
        node = invokeAny(te, new String[] {"getGridNode"}, new Class[0], new Object[0]);
      }
      if (node == null) {
        return null;
      }
      return invokeAny(node, new String[] {"getGrid"}, new Class[0], new Object[0]);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static Iterable<?> storageList(Object grid) {
    try {
      Class<?> storageGridClass = classForName("appeng.api.networking.storage.IStorageGrid");
      Object storageGrid = invokeAny(grid, new String[] {"getCache"}, new Class[] {Class.class}, new Object[] {storageGridClass});
      if (storageGrid == null) {
        return null;
      }
      Object monitor = invokeAny(storageGrid, new String[] {"getItemInventory"}, new Class[0], new Object[0]);
      if (monitor == null) {
        return null;
      }
      Object list = invokeAny(monitor, new String[] {"getStorageList"}, new Class[0], new Object[0]);
      if (list instanceof Iterable) {
        return (Iterable<?>) list;
      }
      return null;
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static void writeNetwork(PrintWriter w, Object grid, Object te, Iterable<?> items) throws Exception {
    int x = intField(te, "xCoord", "field_145851_c");
    int y = intField(te, "yCoord", "field_145848_d");
    int z = intField(te, "zCoord", "field_145849_e");
    int dim = dimensionID(te);
    String label = "ME network " + x + "," + y + "," + z;
    w.print("{\"network_id\":\"");
    w.print(escape(Integer.toHexString(System.identityHashCode(grid))));
    w.print("\",\"label\":\"");
    w.print(escape(label));
    w.print("\",\"dim\":");
    w.print(dim);
    w.print(",\"x\":");
    w.print(x);
    w.print(",\"y\":");
    w.print(y);
    w.print(",\"z\":");
    w.print(z);
    StringBuilder itemJson = new StringBuilder();
    int entryCount = 0;
    int convertedCount = 0;
    int positiveCount = 0;
    String firstEntryClass = "";
    String firstStackClass = "";
    boolean first = true;
    Iterator<?> it = items.iterator();
    while (it.hasNext()) {
      Object entry = it.next();
      entryCount++;
      if (firstEntryClass.length() == 0 && entry != null) {
        firstEntryClass = entry.getClass().getName();
      }
      Object stack = toItemStack(entry);
      if (stack != null) {
        convertedCount++;
        if (firstStackClass.length() == 0) {
          firstStackClass = stack.getClass().getName();
        }
      }
      Object item = stack != null ? invokeAny(stack, new String[] {"getItem", "func_77973_b"}, new Class[0], new Object[0]) : null;
      int count = stack != null ? intField(stack, "stackSize", "field_77994_a") : 0;
      if (stack == null || item == null || count <= 0) {
        continue;
      }
      positiveCount++;
      if (!first) {
        itemJson.append(',');
      }
      first = false;
      itemJson.append(itemJson(stack));
    }
    w.print(",\"entry_count\":");
    w.print(entryCount);
    w.print(",\"converted_count\":");
    w.print(convertedCount);
    w.print(",\"positive_count\":");
    w.print(positiveCount);
    w.print(",\"first_entry_class\":\"");
    w.print(escape(firstEntryClass));
    w.print("\",\"first_stack_class\":\"");
    w.print(escape(firstStackClass));
    w.print("\",\"items\":[");
    w.print(itemJson.toString());
    w.print("]}");
  }

  private static String itemJson(Object stack) {
    StringWriter out = new StringWriter();
    PrintWriter w = new PrintWriter(out);
    writeItem(w, stack);
    w.flush();
    return out.toString();
  }

  private static String itemJson(Object stack, int slot, String source) {
    StringWriter out = new StringWriter();
    PrintWriter w = new PrintWriter(out);
    writeItem(w, stack, slot, source);
    w.flush();
    return out.toString();
  }

  private static Object toItemStack(Object aeStack) {
    try {
      Object stack = invokeAny(aeStack, new String[] {"getItemStack"}, new Class[0], new Object[0]);
      if (stack != null) {
        Object copy = invokeAny(stack, new String[] {"copy", "func_77946_l"}, new Class[0], new Object[0]);
        Object size = invokeAny(aeStack, new String[] {"getStackSize"}, new Class[0], new Object[0]);
        if (size instanceof Number) {
          long n = ((Number) size).longValue();
          setField(copy, n > Integer.MAX_VALUE ? Integer.MAX_VALUE : (int) n, "stackSize", "field_77994_a");
        }
        return copy;
      }
    } catch (Throwable ignored) {
    }
    return null;
  }

  private static void writeItem(PrintWriter w, Object stack) {
    writeItem(w, stack, Integer.MIN_VALUE, "");
  }

  private static void writeItem(PrintWriter w, Object stack, int slot, String source) {
    Object item = invokeQuiet(stack, new String[] {"getItem", "func_77973_b"}, new Class[0], new Object[0]);
    Object uid = uniqueIdentifier(item);
    String reg = "";
    if (uid != null) {
      reg = String.valueOf(getFieldQuiet(uid, "modId")) + ":" + String.valueOf(getFieldQuiet(uid, "name"));
    }
    w.print("{\"id\":");
    w.print(itemID(item));
    w.print(",\"damage\":");
    w.print(intInvoke(stack, "getItemDamage", "func_77960_j"));
    w.print(",\"count\":");
    w.print(intField(stack, "stackSize", "field_77994_a"));
    if (slot != Integer.MIN_VALUE) {
      w.print(",\"slot\":");
      w.print(slot);
    }
    if (source != null && source.length() > 0) {
      w.print(",\"source\":\"");
      w.print(escape(source));
      w.print("\"");
    }
    w.print(",\"reg_name\":\"");
    w.print(escape(reg));
    w.print("\",\"display_name\":\"");
    try {
      w.print(escape(String.valueOf(invokeAny(stack, new String[] {"getDisplayName", "func_82833_r"}, new Class[0], new Object[0]))));
    } catch (Throwable ignored) {
    }
    w.print("\",\"name\":\"");
    try {
      w.print(escape(String.valueOf(invokeAny(item, new String[] {"getUnlocalizedName", "func_77667_c"}, new Class[] {stack.getClass()}, new Object[] {stack}))));
    } catch (Throwable ignored) {
    }
    w.print("\"}");
  }

  private static boolean isPositiveItemStack(Object stack) {
    if (stack == null) {
      return false;
    }
    Object item = invokeQuiet(stack, new String[] {"getItem", "func_77973_b"}, new Class[0], new Object[0]);
    return item != null && intField(stack, "stackSize", "field_77994_a") > 0;
  }

  private static Object copyStackWithCount(Object stack, long count) {
    Object copy = invokeQuiet(stack, new String[] {"copy", "func_77946_l"}, new Class[0], new Object[0]);
    if (copy == null) {
      copy = stack;
    }
    if (count > Integer.MAX_VALUE) {
      count = Integer.MAX_VALUE;
    }
    try {
      setField(copy, Integer.valueOf((int) count), "stackSize", "field_77994_a");
    } catch (Throwable ignored) {
    }
    return copy;
  }

  private static Class<?> classForName(String name) throws ClassNotFoundException {
    synchronized (CLASS_CACHE) {
      Object cached = CLASS_CACHE.get(name);
      if (cached == CACHE_MISS) {
        throw new ClassNotFoundException(name);
      }
      if (cached instanceof Class) {
        return (Class<?>) cached;
      }
    }
    try {
      Class<?> c = Class.forName(name);
      synchronized (CLASS_CACHE) {
        CLASS_CACHE.put(name, c);
      }
      return c;
    } catch (ClassNotFoundException e) {
      synchronized (CLASS_CACHE) {
        CLASS_CACHE.put(name, CACHE_MISS);
      }
      throw e;
    }
  }

  private static Method findMethod(Class<?> c, String name, Class[] types) {
    String key = memberKey(c, name, types);
    synchronized (METHOD_CACHE) {
      Object cached = METHOD_CACHE.get(key);
      if (cached == CACHE_MISS) {
        return null;
      }
      if (cached instanceof Method) {
        return (Method) cached;
      }
    }
    Method m = null;
    try {
      m = c.getMethod(name, types);
    } catch (NoSuchMethodException ignored) {
      try {
        m = c.getDeclaredMethod(name, types);
      } catch (NoSuchMethodException ignoredToo) {
      }
    }
    synchronized (METHOD_CACHE) {
      if (m == null) {
        METHOD_CACHE.put(key, CACHE_MISS);
      } else {
        m.setAccessible(true);
        METHOD_CACHE.put(key, m);
      }
    }
    return m;
  }

  private static Field findField(Class<?> c, String name) {
    String key = c.getName() + "#" + name;
    synchronized (FIELD_CACHE) {
      Object cached = FIELD_CACHE.get(key);
      if (cached == CACHE_MISS) {
        return null;
      }
      if (cached instanceof Field) {
        return (Field) cached;
      }
    }
    Field f = null;
    try {
      f = c.getField(name);
    } catch (NoSuchFieldException ignored) {
      try {
        f = c.getDeclaredField(name);
      } catch (NoSuchFieldException ignoredToo) {
      }
    }
    synchronized (FIELD_CACHE) {
      if (f == null) {
        FIELD_CACHE.put(key, CACHE_MISS);
      } else {
        f.setAccessible(true);
        FIELD_CACHE.put(key, f);
      }
    }
    return f;
  }

  private static String memberKey(Class<?> c, String name, Class[] types) {
    StringBuilder b = new StringBuilder(c.getName());
    b.append('#').append(name).append('(');
    for (int i = 0; i < types.length; i++) {
      if (i > 0) {
        b.append(',');
      }
      b.append(types[i] == null ? "null" : types[i].getName());
    }
    b.append(')');
    return b.toString();
  }

  private static Object invokeAny(Object target, String[] names, Class[] types, Object[] args) throws Exception {
    Class<?> c = target.getClass();
    while (c != null) {
      for (String name : names) {
        Method m = findMethod(c, name, types);
        if (m != null) {
          return m.invoke(target, args);
        }
      }
      c = c.getSuperclass();
    }
    return null;
  }

  private static Object invokeQuiet(Object target, String[] names, Class[] types, Object[] args) {
    try {
      return invokeAny(target, names, types, args);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static Object getField(Object target, String... names) throws Exception {
    Class<?> c = target.getClass();
    while (c != null) {
      for (String name : names) {
        Field f = findField(c, name);
        if (f != null) {
          return f.get(target);
        }
      }
      c = c.getSuperclass();
    }
    return null;
  }

  private static Object getFieldQuiet(Object target, String... names) {
    try {
      return getField(target, names);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static void setField(Object target, Object value, String... names) throws Exception {
    Class<?> c = target.getClass();
    while (c != null) {
      for (String name : names) {
        Field f = findField(c, name);
        if (f != null) {
          f.set(target, value);
          return;
        }
      }
      c = c.getSuperclass();
    }
  }

  private static int intField(Object target, String... names) {
    Object v = getFieldQuiet(target, names);
    return v instanceof Number ? ((Number) v).intValue() : 0;
  }

  private static long longField(Object target, String... names) {
    Object v = getFieldQuiet(target, names);
    return v instanceof Number ? ((Number) v).longValue() : 0L;
  }

  private static int intInvoke(Object target, String... names) {
    Object v = invokeQuiet(target, names, new Class[0], new Object[0]);
    return v instanceof Number ? ((Number) v).intValue() : 0;
  }

  private static int dimensionID(Object te) {
    try {
      Object world = invokeAny(te, new String[] {"getWorldObj", "func_145831_w"}, new Class[0], new Object[0]);
      Object provider = world != null ? getField(world, "provider", "field_73011_w") : null;
      return provider != null ? intField(provider, "dimensionId", "field_76574_g") : 0;
    } catch (Throwable ignored) {
      return 0;
    }
  }

  private static Object uniqueIdentifier(Object item) {
    try {
      Class<?> itemClass = classForName("net.minecraft.item.Item");
      Class<?> registry = classForName("cpw.mods.fml.common.registry.GameRegistry");
      Method m = findMethod(registry, "findUniqueIdentifierFor", new Class[] {itemClass});
      return m != null ? m.invoke(null, item) : null;
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static Object uniqueIdentifierForBlock(Object block) {
    try {
      Class<?> blockClass = classForName("net.minecraft.block.Block");
      Class<?> registry = classForName("cpw.mods.fml.common.registry.GameRegistry");
      Method m = findMethod(registry, "findUniqueIdentifierFor", new Class[] {blockClass});
      return m != null ? m.invoke(null, block) : null;
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static int itemID(Object item) {
    try {
      Class<?> itemClass = classForName("net.minecraft.item.Item");
      Method m = findMethod(itemClass, "getIdFromItem", new Class[] {itemClass});
      if (m == null) {
        m = findMethod(itemClass, "func_150891_b", new Class[] {itemClass});
      }
      if (m == null) {
        return 0;
      }
      Object v = m.invoke(null, item);
      return v instanceof Number ? ((Number) v).intValue() : 0;
    } catch (Throwable ignored) {
      return 0;
    }
  }

  private static Object blockAt(Object world, int x, int y, int z) {
    if (world == null) {
      return null;
    }
    return invokeQuiet(world, new String[] {"getBlock", "func_147439_a"}, new Class[] {int.class, int.class, int.class}, new Object[] {Integer.valueOf(x), Integer.valueOf(y), Integer.valueOf(z)});
  }

  private static int blockMetaAt(Object world, int x, int y, int z) {
    Object v = world == null ? null : invokeQuiet(world, new String[] {"getBlockMetadata", "func_72805_g"}, new Class[] {int.class, int.class, int.class}, new Object[] {Integer.valueOf(x), Integer.valueOf(y), Integer.valueOf(z)});
    return v instanceof Number ? ((Number) v).intValue() : 0;
  }

  private static int blockID(Object block) {
    if (block == null) {
      return 0;
    }
    try {
      Class<?> blockClass = classForName("net.minecraft.block.Block");
      Method m = findMethod(blockClass, "getIdFromBlock", new Class[] {blockClass});
      if (m == null) {
        m = findMethod(blockClass, "func_149682_b", new Class[] {blockClass});
      }
      if (m == null) {
        return 0;
      }
      Object v = m.invoke(null, block);
      return v instanceof Number ? ((Number) v).intValue() : 0;
    } catch (Throwable ignored) {
      return 0;
    }
  }

  private static String blockDisplayName(Object block) {
    if (block == null) {
      return "";
    }
    Object v = invokeQuiet(block, new String[] {"getLocalizedName", "func_149732_F"}, new Class[0], new Object[0]);
    if (v != null && String.valueOf(v).length() > 0) {
      return String.valueOf(v);
    }
    v = invokeQuiet(block, new String[] {"getUnlocalizedName", "func_149739_a"}, new Class[0], new Object[0]);
    return v != null ? String.valueOf(v) : "";
  }

  private static String inventorySource(Object te) {
    try {
      Class<?> sided = classForName("net.minecraft.inventory.ISidedInventory");
      if (sided.isInstance(te)) {
        return "ISidedInventory";
      }
    } catch (Throwable ignored) {
    }
    try {
      Class<?> inv = classForName("net.minecraft.inventory.IInventory");
      if (inv.isInstance(te)) {
        return "IInventory";
      }
    } catch (Throwable ignored) {
    }
    return "tile-fields";
  }

  private static Object gregTechMetaTileEntity(Object te) {
    Object meta = invokeQuiet(te, new String[] {"getMetaTileEntity"}, new Class[0], new Object[0]);
    if (meta != null) {
      return meta;
    }
    return firstNonNull(
        getFieldQuiet(te, "mMetaTileEntity"),
        getFieldQuiet(te, "metaTileEntity"),
        getFieldQuiet(te, "mMetaTile"));
  }

  private static int gregTechMetaID(Object meta) {
    if (meta == null) {
      return 0;
    }
    int id = intInvoke(meta, "getMetaTileID", "getMetaTileId", "getID", "getId");
    if (id > 0) {
      return id;
    }
    return intField(meta, "mID", "mId", "mMetaTileID", "mMetaTileId");
  }

  private static String gregTechMetaName(Object meta) {
    if (meta == null) {
      return "";
    }
    return firstNonEmptyString(
        invokeQuiet(meta, new String[] {"getLocalName"}, new Class[0], new Object[0]),
        invokeQuiet(meta, new String[] {"getInventoryName", "func_145825_b"}, new Class[0], new Object[0]),
        invokeQuiet(meta, new String[] {"getMetaName"}, new Class[0], new Object[0]),
        invokeQuiet(meta, new String[] {"getName"}, new Class[0], new Object[0]),
        getFieldQuiet(meta, "mName"),
        getFieldQuiet(meta, "mLocalName"),
        getFieldQuiet(meta, "mMetaName"));
  }

  private static Object firstNonNull(Object... values) {
    for (Object v : values) {
      if (v != null) {
        return v;
      }
    }
    return null;
  }

  private static String firstNonEmptyString(Object... values) {
    for (Object v : values) {
      if (v == null) {
        continue;
      }
      String s = String.valueOf(v).trim();
      if (s.length() > 0 && !"<nil>".equals(s)) {
        return s;
      }
    }
    return "";
  }

  private static String nowUTC() {
    SimpleDateFormat fmt = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'");
    fmt.setTimeZone(TimeZone.getTimeZone("UTC"));
    return fmt.format(new Date());
  }

  private static String escape(String value) {
    if (value == null) {
      return "";
    }
    StringBuilder b = new StringBuilder(value.length() + 8);
    for (int i = 0; i < value.length(); i++) {
      char ch = value.charAt(i);
      switch (ch) {
        case '\\':
          b.append("\\\\");
          break;
        case '"':
          b.append("\\\"");
          break;
        case '\n':
        case '\r':
        case '\t':
          b.append(' ');
          break;
        default:
          b.append(ch);
      }
    }
    return b.toString().trim();
  }
}
