package greggpt.recipedump;

import cpw.mods.fml.common.Loader;
import cpw.mods.fml.common.LoaderState;
import cpw.mods.fml.common.Mod;
import cpw.mods.fml.common.Mod.EventHandler;
import cpw.mods.fml.common.event.FMLLoadCompleteEvent;
import cpw.mods.fml.common.registry.GameRegistry;
import java.io.BufferedWriter;
import java.io.File;
import java.io.IOException;
import java.lang.reflect.Array;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.net.URL;
import java.net.URLClassLoader;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Savepoint;
import java.sql.Statement;
import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.Comparator;
import java.util.ConcurrentModificationException;
import java.util.IdentityHashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.atomic.AtomicBoolean;

@Mod(
    modid = "greggpt_recipe_dump",
    name = "GregGPT Recipe Dump",
    version = "2.0.0",
    acceptableRemoteVersions = "*"
)
public final class GregGPTRecipeDumpMod {
  private static final AtomicBoolean WORKER_STARTED = new AtomicBoolean(false);
  private static final String DUMP_FILE_NAME = "greggpt_recipes.sqlite";
  private static final String LOG_FILE_NAME = "greggpt_recipe_dump.log";

  public GregGPTRecipeDumpMod() {
    appendLog("[GREGGPT] recipe dump mod constructed; waiting for load-complete");
  }

  @EventHandler
  public void onLoadComplete(FMLLoadCompleteEvent event) {
    startDumpWorker("load-complete");
  }

  private static void startDumpWorker(final String trigger) {
    if (!WORKER_STARTED.compareAndSet(false, true)) {
      appendLog("[GREGGPT] recipe dump worker already started; ignoring trigger " + trigger);
      return;
    }

    Thread worker =
        new Thread(
            new Runnable() {
              @Override
              public void run() {
                runDumpWorker(trigger);
              }
            },
            "greggpt-recipe-dump");
    worker.setDaemon(true);
    worker.start();
  }

  private static void runDumpWorker(String trigger) {
    File outFile = new File(getDumpsDir(), DUMP_FILE_NAME);
    appendLog("[GREGGPT] recipe dump worker started from " + trigger);

    for (int attempt = 1; attempt <= 300; attempt++) {
      Loader loader = Loader.instance();
      LoaderState state = loader != null ? loader.getLoaderState() : null;
      boolean available = loader != null && loader.hasReachedState(LoaderState.AVAILABLE);
      appendLog(
          "[GREGGPT] recipe dump attempt "
              + attempt
              + " state="
              + (state != null ? state.name() : "null"));

      if (available) {
        try {
          writeDump(outFile);
          appendLog("[GREGGPT] wrote recipe dump to " + outFile.getAbsolutePath());
          System.out.println("[GREGGPT] wrote recipe dump to " + outFile.getAbsolutePath());
          sleepQuietly(1500L);
          try {
            Runtime.getRuntime().halt(0);
          } catch (SecurityException trappedExit) {
            appendLog("[GREGGPT] dump complete; Forge blocked automatic process exit");
          }
          return;
        } catch (Throwable t) {
          appendThrowable("[GREGGPT] failed to write recipe dump", t);
          throw new RuntimeException("failed to write recipe dump", t);
        }
      }

      sleepQuietly(1000L);
    }

    throw new RuntimeException("timed out waiting for Forge to become available");
  }

  private static void writeDump(File outFile) throws Exception {
    loadSqliteDriver();
    if (outFile.exists() && !outFile.delete()) {
      throw new IOException("failed to replace existing dump: " + outFile);
    }

    Connection conn = DriverManager.getConnection("jdbc:sqlite:" + outFile.getAbsolutePath());
    try {
      configureConnection(conn);
      conn.setAutoCommit(false);
      Db db = new Db(conn);
      db.createSchema();
      db.putManifest("schema_version", "2");
      db.putManifest("mod_version", "2.0.0");
      db.putManifest("minecraft_version", "1.7.10");
      db.putManifest("dump_file", outFile.getAbsolutePath());
      db.putManifest("started_at_millis", String.valueOf(System.currentTimeMillis()));

      VanillaDumper vanilla = new VanillaDumper(db);
      GregTechDumper gregTech = new GregTechDumper(db);
      WorldgenDumper worldgen = new WorldgenDumper(db);
      int vanillaCount = vanilla.dump();
      int gregTechCount = gregTech.dump();
      worldgen.dump();
      db.finishSchema();

      db.putManifest("vanilla_recipe_count", String.valueOf(vanillaCount));
      db.putManifest("crafting_recipe_count", String.valueOf(vanilla.craftingCount));
      db.putManifest("furnace_recipe_count", String.valueOf(vanilla.furnaceCount));
      db.putManifest("vanilla_recipe_error_count", String.valueOf(vanilla.errorCount));
      db.putManifest("gregtech_recipe_count", String.valueOf(gregTechCount));
      db.putManifest("gregtech_recipe_error_count", String.valueOf(gregTech.errorCount));
      db.putManifest("ore_vein_count", String.valueOf(worldgen.veinCount));
      db.putManifest("small_ore_count", String.valueOf(worldgen.smallOreCount));
      db.putManifest("worldgen_error_count", String.valueOf(worldgen.errorCount));
      db.putManifest("worldgen_data_available", worldgen.errorCount == 0 && worldgen.veinCount > 0 ? "1" : "0");
      boolean sourceCoverageComplete =
          vanilla.craftingCount > 0
              && vanilla.furnaceCount > 0
              && gregTechCount > 0
              && worldgen.veinCount > 0;
      db.putManifest(
          "dump_complete",
          sourceCoverageComplete
                  && vanilla.errorCount == 0
                  && gregTech.errorCount == 0
                  && worldgen.errorCount == 0
              ? "1"
              : "0");
      db.putManifest("finished_at_millis", String.valueOf(System.currentTimeMillis()));
      conn.commit();
    } catch (Throwable t) {
      rollbackQuietly(conn);
      throw t;
    } finally {
      conn.close();
    }
  }

  private static void configureConnection(Connection conn) throws SQLException {
    Statement st = conn.createStatement();
    try {
      st.executeUpdate("PRAGMA foreign_keys=ON");
      st.executeUpdate("PRAGMA journal_mode=OFF");
      st.executeUpdate("PRAGMA synchronous=OFF");
    } finally {
      st.close();
    }
  }

  private static void loadSqliteDriver() throws Exception {
    try {
      Class.forName("org.sqlite.JDBC");
      return;
    } catch (ClassNotFoundException ignored) {
    }

    String jarPath = System.getProperty("greggpt.sqliteJdbcJar");
    if (jarPath == null || jarPath.trim().isEmpty()) {
      jarPath = System.getenv("GREGGPT_SQLITE_JDBC_JAR");
    }
    if (jarPath == null || jarPath.trim().isEmpty()) {
      File jar = findBundledSqliteJdbcJar();
      if (jar != null) {
        jarPath = jar.getAbsolutePath();
      }
    }
    if (jarPath != null && !jarPath.trim().isEmpty()) {
      File jar = new File(jarPath.trim());
      URL url = jar.toURI().toURL();
      ClassLoader parent = Thread.currentThread().getContextClassLoader();
      URLClassLoader loader = new URLClassLoader(new URL[] {url}, parent);
      Class.forName("org.sqlite.JDBC", true, loader);
      return;
    }

    Class.forName("org.sqlite.JDBC");
  }

  private static File findBundledSqliteJdbcJar() {
    File baseDir = getDumpsDir().getParentFile();
    File modsDir = baseDir == null ? null : new File(baseDir, "mods");
    File[] candidates = modsDir == null ? null : modsDir.listFiles();
    if (candidates == null) {
      return null;
    }
    for (int i = 0; i < candidates.length; i++) {
      String name = candidates[i].getName().toLowerCase(Locale.ROOT);
      if (candidates[i].isFile() && name.startsWith("sqlite-jdbc") && name.endsWith(".jar")) {
        return candidates[i];
      }
    }
    return null;
  }

  private static final class VanillaDumper {
    private final Db db;
    private final Accessors accessors = new Accessors();
    private int craftingCount;
    private int furnaceCount;
    private int errorCount;

    private VanillaDumper(Db db) {
      this.db = db;
    }

    private int dump() {
      craftingCount = dumpCrafting();
      furnaceCount = dumpFurnace();
      if (craftingCount == 0) {
        errorCount++;
        db.recordDumpError("crafting", null, "minecraft.crafting", "no crafting recipes discovered");
      }
      if (furnaceCount == 0) {
        errorCount++;
        db.recordDumpError("furnace", null, "minecraft.furnace", "no furnace recipes discovered");
      }
      return craftingCount + furnaceCount;
    }

    private int dumpCrafting() {
      int count = 0;
      try {
        Object craftingManager =
            invokeStatic(
                classForName("net.minecraft.item.crafting.CraftingManager"),
                "getInstance",
                "func_77594_a");
        Object recipes = invokeAny(craftingManager, "getRecipeList", "func_77592_b");
        int ordinal = 0;
        for (Object recipe : iterable(recipes)) {
          if (recipe == null) {
            ordinal++;
            continue;
          }
          Savepoint savepoint = db.beginRecipe();
          try {
            Object output = invokeAny(recipe, "getRecipeOutput", "func_77571_b");
            if (isEmptyStack(output)) {
              db.rollbackRecipe(savepoint);
              ordinal++;
              continue;
            }
            String handlerName = recipe.getClass().getName();
            int recipeId = db.insertRecipe(handlerName, handlerName + "#" + ordinal, "crafting", null, null, null, false, false, true);
            db.insertMetadata(recipeId, "class", recipe.getClass().getName());
            int inputCount = dumpCraftingInputs(recipeId, recipe);
            if (inputCount == 0) {
              throw new SQLException("crafting recipe has no dumpable inputs");
            }
            db.insertOutput(recipeId, 0, "item", accessors.stackSize(output), db.itemId(output), null, Integer.valueOf(10000), null);
            db.releaseRecipe(savepoint);
            count++;
          } catch (Throwable t) {
            db.rollbackRecipe(savepoint);
            db.recordDumpError("crafting", recipe.getClass().getName() + "#" + ordinal, recipe.getClass().getName(), throwableSummary(t));
            errorCount++;
            appendThrowable("[GREGGPT] failed to dump crafting recipe " + recipe.getClass().getName() + "#" + ordinal, t);
          }
          ordinal++;
        }
      } catch (Throwable t) {
        errorCount++;
        db.recordDumpError("crafting_source", null, "minecraft.crafting", throwableSummary(t));
        appendThrowable("[GREGGPT] vanilla crafting dump failed", t);
      }
      return count;
    }

    private int dumpCraftingInputs(int recipeId, Object recipe) throws Exception {
      Object inputs = firstFieldValue(recipe, "recipeItems", "field_77574_d", "input", "items");
      if (inputs == null) {
        inputs = firstFieldValue(recipe, "recipeItems", "field_77579_b");
      }
      int pos = 0;
      int count = 0;
      for (Object input : iterable(inputs)) {
        if (dumpInputObject(recipeId, pos, input)) {
          count++;
        }
        pos++;
      }
      return count;
    }

    private int dumpFurnace() {
      int count = 0;
      try {
        Object furnace = invokeStatic(classForName("net.minecraft.item.crafting.FurnaceRecipes"), "smelting", "func_77602_a");
        Object map = invokeAny(furnace, "getSmeltingList", "func_77599_b");
        if (!(map instanceof Map)) {
          return 0;
        }
        int ordinal = 0;
        for (Object entryObj : snapshotMapEntries((Map<?, ?>) map)) {
          Map.Entry<?, ?> entry = (Map.Entry<?, ?>) entryObj;
          Object input = entry.getKey();
          Object output = entry.getValue();
          if (isEmptyStack(input) || isEmptyStack(output)) {
            ordinal++;
            continue;
          }
          Savepoint savepoint = db.beginRecipe();
          try {
            int recipeId = db.insertRecipe("minecraft.furnace", "minecraft.furnace#" + ordinal, "furnace", null, null, null, false, false, true);
            if (!dumpInputObject(recipeId, 0, input)) {
              throw new SQLException("furnace recipe has no dumpable input");
            }
            db.insertOutput(recipeId, 0, "item", accessors.stackSize(output), db.itemId(output), null, Integer.valueOf(10000), null);
            db.releaseRecipe(savepoint);
            count++;
          } catch (Throwable t) {
            db.rollbackRecipe(savepoint);
            db.recordDumpError("furnace", "minecraft.furnace#" + ordinal, "minecraft.furnace", throwableSummary(t));
            errorCount++;
            appendThrowable("[GREGGPT] failed to dump furnace recipe #" + ordinal, t);
          }
          ordinal++;
        }
      } catch (Throwable t) {
        errorCount++;
        db.recordDumpError("furnace_source", null, "minecraft.furnace", throwableSummary(t));
        appendThrowable("[GREGGPT] vanilla furnace dump failed", t);
      }
      return count;
    }

    private boolean dumpInputObject(int recipeId, int position, Object input) throws Exception {
      if (input == null || isEmptyStack(input)) {
        return false;
      }
      if (isItemStack(input)) {
        int inputId = db.insertInput(recipeId, position, "item", accessors.stackSize(input), null);
        db.insertInputOption(inputId, 0, "item", db.itemId(input), null, null, null, null, accessors.stackSize(input));
        return true;
      }
      if (input instanceof String) {
        db.insertInput(recipeId, position, "oredict", 1, input.toString());
        return true;
      }
      if (input instanceof Collection || input.getClass().isArray()) {
        List<Object> options = iterable(input);
        if (options.isEmpty()) {
          return false;
        }
        int inputId = db.insertInput(recipeId, position, "item", 1, null);
        int ordinal = 0;
        for (Object option : options) {
          if (isItemStack(option) && !isEmptyStack(option)) {
            db.insertInputOption(inputId, ordinal, "item", db.itemId(option), null, null, null, null, accessors.stackSize(option));
            ordinal++;
          } else if (option instanceof String) {
            db.insertInputOption(inputId, ordinal, "oredict", null, null, null, option.toString(), null, 1);
            ordinal++;
          }
        }
        return ordinal > 0;
      }
      db.insertInput(recipeId, position, "unknown", 1, input.getClass().getName());
      return true;
    }
  }

  private static final class GregTechDumper {
    private final Db db;
    private final Accessors accessors = new Accessors();
    private int errorCount;

    private GregTechDumper(Db db) {
      this.db = db;
    }

    private int dump() {
      List<Object> maps = discoverRecipeMaps();
      int count = 0;
      for (Object recipeMap : maps) {
        String handlerName = recipeMapName(recipeMap);
        Collection<?> recipes = new ArrayList<Object>(recipeList(recipeMap));
        int ordinal = 0;
        for (Object recipe : recipes) {
          if (recipe == null) {
            continue;
          }
          Savepoint savepoint = null;
          try {
            savepoint = db.beginRecipe();
            String key = handlerName + "#" + ordinal;
            Integer duration = intField(recipe, "mDuration");
            Integer eut = intField(recipe, "mEUt");
            Integer special = intField(recipe, "mSpecialValue");
            boolean fake = booleanFieldTrue(recipe, "mFakeRecipe");
            boolean hidden = booleanFieldTrue(recipe, "mHidden") || fake;
            Boolean enabledValue = boolField(recipe, "mEnabled");
            boolean enabled = enabledValue == null || enabledValue.booleanValue();
            int recipeId = db.insertRecipe(handlerName, key, "gregtech", duration, eut, special, hidden, fake, enabled);
            db.insertMetadata(recipeId, "class", recipe.getClass().getName());
            db.insertMetadata(recipeId, "map_class", recipeMap.getClass().getName());
            int nextInputPosition = dumpItems(recipeId, fieldValue(recipe, "mInputs"), true, 0);
            dumpFluids(recipeId, fieldValue(recipe, "mFluidInputs"), true, nextInputPosition);
            int nextOutputPosition = dumpItems(recipeId, fieldValue(recipe, "mOutputs"), false, 0);
            dumpFluids(recipeId, fieldValue(recipe, "mFluidOutputs"), false, nextOutputPosition);
            dumpChances(recipeId, fieldValue(recipe, "mChances"));
            db.releaseRecipe(savepoint);
            count++;
          } catch (Throwable t) {
            db.rollbackRecipe(savepoint);
            db.recordDumpError("gregtech", handlerName + "#" + ordinal, handlerName, throwableSummary(t));
            errorCount++;
            appendThrowable("[GREGGPT] failed to dump GregTech recipe from " + handlerName, t);
          }
          ordinal++;
        }
      }
      return count;
    }

    private int dumpItems(int recipeId, Object stacks, boolean input, int startPosition) throws Exception {
      int pos = startPosition;
      for (Object stack : iterable(stacks)) {
        if (isEmptyStack(stack)) {
          pos++;
          continue;
        }
        if (input) {
          int inputId = db.insertInput(recipeId, pos, "item", accessors.stackSize(stack), null);
          db.insertInputOption(inputId, 0, "item", db.itemId(stack), null, null, null, null, accessors.stackSize(stack));
        } else {
          db.insertOutput(recipeId, pos, "item", accessors.stackSize(stack), db.itemId(stack), null, Integer.valueOf(10000), null);
        }
        pos++;
      }
      return pos;
    }

    private int dumpFluids(int recipeId, Object fluids, boolean input, int startPosition) throws Exception {
      int pos = startPosition;
      for (Object fluidStack : iterable(fluids)) {
        if (fluidStack == null) {
          pos++;
          continue;
        }
        int amount = fluidAmount(fluidStack);
        int fluidId = db.fluidId(fluidStack);
        if (input) {
          int inputId = db.insertInput(recipeId, pos, "fluid", amount, null);
          db.insertInputOption(inputId, 0, "fluid", null, fluidId, null, null, null, amount);
        } else {
          db.insertOutput(recipeId, pos, "fluid", amount, null, fluidId, Integer.valueOf(10000), null);
        }
        pos++;
      }
      return pos;
    }

    private void dumpChances(int recipeId, Object chances) throws SQLException {
      int pos = 0;
      for (Object chance : iterable(chances)) {
        if (chance != null) {
          db.updateOutputChance(recipeId, pos, Integer.valueOf(chance.toString()));
          db.insertMetadata(recipeId, "output_chance_" + pos, chance.toString());
        }
        pos++;
      }
    }

    private static List<Object> discoverRecipeMaps() {
      IdentityHashMap<Object, Boolean> seen = new IdentityHashMap<Object, Boolean>();
      List<Object> maps = new ArrayList<Object>();
      inspectRecipeMapHolder("gregtech.api.recipe.RecipeMaps", seen, maps);
      inspectRecipeMapHolder("gregtech.api.enums.GT_Values", seen, maps);
      inspectRecipeMapHolder("gregtech.api.util.GT_Recipe$GT_Recipe_Map", seen, maps);
      Collections.sort(
          maps,
          new Comparator<Object>() {
            @Override
            public int compare(Object left, Object right) {
              return recipeMapName(left).compareTo(recipeMapName(right));
            }
          });
      appendLog("[GREGGPT] discovered " + maps.size() + " GregTech recipe maps");
      return maps;
    }

    private static void inspectRecipeMapHolder(String className, IdentityHashMap<Object, Boolean> seen, List<Object> maps) {
      Class<?> type;
      try {
        type = classForName(className);
      } catch (Throwable ignored) {
        return;
      }
      Field[] fields = type.getDeclaredFields();
      for (int i = 0; i < fields.length; i++) {
        Field field = fields[i];
        if ((field.getModifiers() & java.lang.reflect.Modifier.STATIC) == 0) {
          continue;
        }
        try {
          field.setAccessible(true);
          Object value = field.get(null);
          inspectRecipeMapValue(value, seen, maps);
        } catch (Throwable ignored) {
        }
      }
    }

    private static void inspectRecipeMapValue(Object value, IdentityHashMap<Object, Boolean> seen, List<Object> maps) {
      if (value == null) {
        return;
      }
      if (seen.containsKey(value)) {
        return;
      }
      Collection<?> recipes = recipeList(value);
      if (recipes != null) {
        seen.put(value, Boolean.TRUE);
        maps.add(value);
        return;
      }
      if (value instanceof Map) {
        for (Object child : ((Map<?, ?>) value).values()) {
          inspectRecipeMapValue(child, seen, maps);
        }
      } else if (value instanceof Collection || value.getClass().isArray()) {
        for (Object child : iterable(value)) {
          inspectRecipeMapValue(child, seen, maps);
        }
      }
    }

    private static Collection<?> recipeList(Object recipeMap) {
      Object list = fieldValue(recipeMap, "mRecipeList");
      if (list == null) {
        list = fieldValue(recipeMap, "mRecipeListBackend");
      }
      if (list == null) {
        try {
          list = invokeAny(recipeMap, "getAllRecipes");
        } catch (Throwable ignored) {
        }
      }
      if (list instanceof Collection) {
        return (Collection<?>) list;
      }
      if (list instanceof Map) {
        return ((Map<?, ?>) list).values();
      }
      return null;
    }

    private static String recipeMapName(Object recipeMap) {
      String[] fields = {"mUnlocalizedName", "mName", "mNEIName", "unlocalizedName", "name"};
      for (int i = 0; i < fields.length; i++) {
        Object value = fieldValue(recipeMap, fields[i]);
        if (value != null && !value.toString().trim().isEmpty()) {
          return value.toString();
        }
      }
      return recipeMap.getClass().getName() + "@" + Integer.toHexString(System.identityHashCode(recipeMap));
    }
  }

  private static final class WorldgenDumper {
    private final Db db;
    private int veinCount;
    private int smallOreCount;
    private int errorCount;

    private WorldgenDumper(Db db) {
      this.db = db;
    }

    private void dump() {
      veinCount = dumpVeins();
      smallOreCount = dumpSmallOres();
    }

    private int dumpVeins() {
      int count = 0;
      try {
        Class<?> layerClass = classForName("gregtech.common.WorldgenGTOreLayer");
        Object layers = findField(layerClass, "sList").get(null);
        for (Object layer : iterable(layers)) {
          if (layer == null) {
            continue;
          }
          Savepoint savepoint = db.beginRecipe();
          String name = worldgenName(layer);
          try {
            List<String> dimensions = stringValues(invokeAny(layer, "getAllowedDimensions"));
            int defaultMinY = intValue(invokeOneArg(layer, "getMinY", String.class, ""));
            int defaultMaxY = intValue(invokeOneArg(layer, "getMaxY", String.class, ""));
            int veinId = db.insertOreVein(
                "vein:" + normalizedWorldgenKey(name),
                name,
                friendlyWorldgenName(name),
                defaultMinY,
                defaultMaxY,
                numberField(layer, "mWeight"),
                numberField(layer, "mDensity"),
                numberField(layer, "mSize"));
            db.insertOreVeinMaterial(veinId, "primary", fieldValue(layer, "mPrimary"));
            db.insertOreVeinMaterial(veinId, "secondary", fieldValue(layer, "mSecondary"));
            db.insertOreVeinMaterial(veinId, "between", fieldValue(layer, "mBetween"));
            db.insertOreVeinMaterial(veinId, "sporadic", fieldValue(layer, "mSporadic"));
            for (String dimension : dimensions) {
              int minY = intValue(invokeOneArg(layer, "getMinY", String.class, dimension));
              int maxY = intValue(invokeOneArg(layer, "getMaxY", String.class, dimension));
              db.insertOreVeinDimension(veinId, dimension, worldgenDimensionName(dimension), minY, maxY);
            }
            db.releaseRecipe(savepoint);
            count++;
          } catch (Throwable t) {
            db.rollbackRecipe(savepoint);
            db.recordDumpError("worldgen_vein", name, null, throwableSummary(t));
            errorCount++;
            appendThrowable("[GREGGPT] failed to dump ore vein " + name, t);
          }
        }
      } catch (Throwable t) {
        errorCount++;
        db.recordDumpError("worldgen_vein_source", null, null, throwableSummary(t));
        appendThrowable("[GREGGPT] ore vein discovery failed", t);
      }
      return count;
    }

    private int dumpSmallOres() {
      int count = 0;
      try {
        Class<?> smallOreClass = classForName("gregtech.common.WorldgenGTOreSmallPieces");
        Object smallOres = findField(smallOreClass, "sList").get(null);
        for (Object smallOre : iterable(smallOres)) {
          if (smallOre == null) {
            continue;
          }
          Savepoint savepoint = db.beginRecipe();
          String name = worldgenName(smallOre);
          try {
            Object material = invokeAny(smallOre, "getMaterial");
            int smallOreId = db.insertSmallOre(
                "small_ore:" + normalizedWorldgenKey(name),
                name,
                material,
                numberField(smallOre, "mMinY"),
                numberField(smallOre, "mMaxY"),
                numberField(smallOre, "mAmount"));
            for (String dimension : stringValues(invokeAny(smallOre, "getAllowedDimensions"))) {
              db.insertSmallOreDimension(smallOreId, dimension, worldgenDimensionName(dimension));
            }
            db.releaseRecipe(savepoint);
            count++;
          } catch (Throwable t) {
            db.rollbackRecipe(savepoint);
            db.recordDumpError("worldgen_small_ore", name, null, throwableSummary(t));
            errorCount++;
            appendThrowable("[GREGGPT] failed to dump small ore " + name, t);
          }
        }
      } catch (Throwable t) {
        errorCount++;
        db.recordDumpError("worldgen_small_ore_source", null, null, throwableSummary(t));
        appendThrowable("[GREGGPT] small ore discovery failed", t);
      }
      return count;
    }

    private static String worldgenName(Object worldgen) {
      try {
        Object value = invokeAny(worldgen, "getName");
        if (value != null && !value.toString().trim().isEmpty()) {
          String name = value.toString().trim();
          int separator = name.lastIndexOf('.');
          if (separator >= 0 && separator + 1 < name.length()) {
            name = name.substring(separator + 1);
          }
          return name;
        }
      } catch (Throwable ignored) {
      }
      return worldgen.getClass().getSimpleName();
    }

    private static String worldgenDimensionName(String dimensionKey) {
      try {
        Class<?> dimensionDef = classForName("galacticgreg.api.enums.DimensionDef");
        Object resolved = findMethod(dimensionDef, "getDefByName", String.class).invoke(null, dimensionKey);
        for (Object enumValue : iterable(invokeStatic(dimensionDef, "values"))) {
          Object definition = fieldValue(enumValue, "modDimensionDef");
          if (definition == null) {
            continue;
          }
          Object identifier = invokeAny(definition, "getDimIdentifier");
          Object registeredName = invokeAny(definition, "getDimensionName");
          if (definition == resolved
              || (identifier != null && dimensionKey.equalsIgnoreCase(identifier.toString()))
              || (registeredName != null && dimensionKey.equalsIgnoreCase(registeredName.toString()))) {
            return friendlyWorldgenName(String.valueOf(invokeAny(enumValue, "name")));
          }
        }
      } catch (Throwable ignored) {
      }
      return friendlyWorldgenName(dimensionKey);
    }
  }

  private static final class Db {
    private final Connection conn;
    private final Map<String, Integer> itemIds = new LinkedHashMap<String, Integer>();
    private final Map<String, Integer> fluidIds = new LinkedHashMap<String, Integer>();
    private final Map<String, Integer> handlerIds = new LinkedHashMap<String, Integer>();
    private final Map<String, Integer> oreMaterialIds = new LinkedHashMap<String, Integer>();
    private final Accessors accessors = new Accessors();

    private PreparedStatement putManifest;
    private PreparedStatement insertItem;
    private PreparedStatement selectItem;
    private PreparedStatement insertFluid;
    private PreparedStatement selectFluid;
    private PreparedStatement insertHandler;
    private PreparedStatement selectHandler;
    private PreparedStatement insertRecipe;
    private PreparedStatement insertInput;
    private PreparedStatement insertInputOption;
    private PreparedStatement insertOutput;
    private PreparedStatement updateOutputChance;
    private PreparedStatement insertMetadata;
    private PreparedStatement insertDumpError;
    private PreparedStatement insertOreMaterial;
    private PreparedStatement selectOreMaterial;
    private PreparedStatement insertOreVein;
    private PreparedStatement insertOreVeinMaterial;
    private PreparedStatement insertOreVeinDimension;
    private PreparedStatement insertSmallOre;
    private PreparedStatement insertSmallOreDimension;

    private Db(Connection conn) {
      this.conn = conn;
    }

    private void createSchema() throws SQLException {
      Statement st = conn.createStatement();
      try {
        st.executeUpdate("CREATE TABLE manifest (key TEXT PRIMARY KEY, value TEXT NOT NULL)");
        st.executeUpdate(
            "CREATE TABLE items (id INTEGER PRIMARY KEY AUTOINCREMENT, registry_name TEXT NOT NULL, damage INTEGER NOT NULL, display_name TEXT, unlocalized_name TEXT, max_damage INTEGER, UNIQUE(registry_name, damage))");
        st.executeUpdate(
            "CREATE TABLE fluids (id INTEGER PRIMARY KEY AUTOINCREMENT, fluid_name TEXT NOT NULL UNIQUE, localized_name TEXT)");
        st.executeUpdate(
            "CREATE TABLE ore_dict_entries (ore_name TEXT NOT NULL, item_id INTEGER NOT NULL, PRIMARY KEY(ore_name, item_id), FOREIGN KEY(item_id) REFERENCES items(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_handlers (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE)");
        st.executeUpdate(
            "CREATE TABLE machine_capabilities (id INTEGER PRIMARY KEY AUTOINCREMENT, handler_id INTEGER NOT NULL UNIQUE, capability_key TEXT NOT NULL, machine_name_hint TEXT NOT NULL, FOREIGN KEY(handler_id) REFERENCES recipe_handlers(id))");
        st.executeUpdate(
            "CREATE TABLE machine_options (id INTEGER PRIMARY KEY AUTOINCREMENT, capability_id INTEGER NOT NULL, item_id INTEGER, block_registry_name TEXT, block_meta INTEGER, display_name TEXT NOT NULL, tier_name TEXT, min_eut INTEGER, max_eut INTEGER, source TEXT NOT NULL, FOREIGN KEY(capability_id) REFERENCES machine_capabilities(id), FOREIGN KEY(item_id) REFERENCES items(id))");
        st.executeUpdate(
            "CREATE TABLE recipes (id INTEGER PRIMARY KEY AUTOINCREMENT, handler_id INTEGER NOT NULL, recipe_key TEXT NOT NULL UNIQUE, category TEXT NOT NULL, duration_ticks INTEGER, eut INTEGER, special_value INTEGER, hidden INTEGER NOT NULL DEFAULT 0, fake INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1, valid INTEGER NOT NULL DEFAULT 1, FOREIGN KEY(handler_id) REFERENCES recipe_handlers(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_inputs (id INTEGER PRIMARY KEY AUTOINCREMENT, recipe_id INTEGER NOT NULL, position INTEGER NOT NULL, kind TEXT NOT NULL, amount INTEGER, label TEXT, consumed INTEGER NOT NULL DEFAULT 1, catalyst INTEGER NOT NULL DEFAULT 0, FOREIGN KEY(recipe_id) REFERENCES recipes(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_input_options (id INTEGER PRIMARY KEY AUTOINCREMENT, input_id INTEGER NOT NULL, option_index INTEGER NOT NULL, kind TEXT NOT NULL, item_id INTEGER, fluid_id INTEGER, amount INTEGER, ore_name TEXT, label TEXT, FOREIGN KEY(input_id) REFERENCES recipe_inputs(id), FOREIGN KEY(item_id) REFERENCES items(id), FOREIGN KEY(fluid_id) REFERENCES fluids(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_outputs (id INTEGER PRIMARY KEY AUTOINCREMENT, recipe_id INTEGER NOT NULL, position INTEGER NOT NULL, kind TEXT NOT NULL, amount INTEGER, item_id INTEGER, fluid_id INTEGER, chance INTEGER NOT NULL DEFAULT 10000, is_primary INTEGER NOT NULL DEFAULT 0, label TEXT, FOREIGN KEY(recipe_id) REFERENCES recipes(id), FOREIGN KEY(item_id) REFERENCES items(id), FOREIGN KEY(fluid_id) REFERENCES fluids(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_edges (id INTEGER PRIMARY KEY AUTOINCREMENT, recipe_id INTEGER NOT NULL, direction TEXT NOT NULL CHECK(direction IN ('input','output')), position INTEGER NOT NULL, option_index INTEGER, resource_kind TEXT NOT NULL, resource_key TEXT NOT NULL, resource_name TEXT, item_id INTEGER, fluid_id INTEGER, ore_name TEXT, amount INTEGER, chance INTEGER NOT NULL DEFAULT 10000, expected_amount REAL, is_primary INTEGER NOT NULL DEFAULT 0, consumed INTEGER NOT NULL DEFAULT 1, catalyst INTEGER NOT NULL DEFAULT 0, FOREIGN KEY(recipe_id) REFERENCES recipes(id), FOREIGN KEY(item_id) REFERENCES items(id), FOREIGN KEY(fluid_id) REFERENCES fluids(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_metadata (id INTEGER PRIMARY KEY AUTOINCREMENT, recipe_id INTEGER NOT NULL, key TEXT NOT NULL, value TEXT, FOREIGN KEY(recipe_id) REFERENCES recipes(id))");
        st.executeUpdate(
            "CREATE TABLE dump_errors (id INTEGER PRIMARY KEY AUTOINCREMENT, category TEXT NOT NULL, recipe_key TEXT, handler_name TEXT, message TEXT NOT NULL)");
        st.executeUpdate(
            "CREATE TABLE ore_materials (id INTEGER PRIMARY KEY AUTOINCREMENT, material_key TEXT NOT NULL UNIQUE, internal_name TEXT NOT NULL UNIQUE, localized_name TEXT)");
        st.executeUpdate(
            "CREATE TABLE ore_veins (id INTEGER PRIMARY KEY AUTOINCREMENT, vein_key TEXT NOT NULL, internal_name TEXT NOT NULL, display_name TEXT NOT NULL, min_y INTEGER NOT NULL, max_y INTEGER NOT NULL, weight INTEGER NOT NULL, density INTEGER NOT NULL, size INTEGER NOT NULL)");
        st.executeUpdate(
            "CREATE TABLE ore_vein_materials (vein_id INTEGER NOT NULL, role TEXT NOT NULL CHECK(role IN ('primary','secondary','between','sporadic')), material_id INTEGER NOT NULL, PRIMARY KEY(vein_id, role), FOREIGN KEY(vein_id) REFERENCES ore_veins(id), FOREIGN KEY(material_id) REFERENCES ore_materials(id))");
        st.executeUpdate(
            "CREATE TABLE ore_vein_dimensions (vein_id INTEGER NOT NULL, dimension_key TEXT NOT NULL, dimension_name TEXT NOT NULL, min_y INTEGER NOT NULL, max_y INTEGER NOT NULL, PRIMARY KEY(vein_id, dimension_key), FOREIGN KEY(vein_id) REFERENCES ore_veins(id))");
        st.executeUpdate(
            "CREATE TABLE small_ores (id INTEGER PRIMARY KEY AUTOINCREMENT, small_ore_key TEXT NOT NULL, internal_name TEXT NOT NULL, material_id INTEGER NOT NULL, min_y INTEGER NOT NULL, max_y INTEGER NOT NULL, amount_per_chunk INTEGER NOT NULL, FOREIGN KEY(material_id) REFERENCES ore_materials(id))");
        st.executeUpdate(
            "CREATE TABLE small_ore_dimensions (small_ore_id INTEGER NOT NULL, dimension_key TEXT NOT NULL, dimension_name TEXT NOT NULL, PRIMARY KEY(small_ore_id, dimension_key), FOREIGN KEY(small_ore_id) REFERENCES small_ores(id))");
        st.executeUpdate("CREATE INDEX idx_items_registry_damage ON items(registry_name, damage)");
        st.executeUpdate("CREATE INDEX idx_items_display_name ON items(display_name COLLATE NOCASE)");
        st.executeUpdate("CREATE INDEX idx_fluids_localized_name ON fluids(localized_name COLLATE NOCASE)");
        st.executeUpdate("CREATE INDEX idx_recipes_handler ON recipes(handler_id)");
        st.executeUpdate("CREATE INDEX idx_recipes_usable ON recipes(valid, hidden, fake, enabled)");
        st.executeUpdate("CREATE INDEX idx_recipe_inputs_recipe ON recipe_inputs(recipe_id)");
        st.executeUpdate("CREATE INDEX idx_recipe_input_options_input ON recipe_input_options(input_id)");
        st.executeUpdate("CREATE INDEX idx_recipe_input_options_item ON recipe_input_options(item_id)");
        st.executeUpdate("CREATE INDEX idx_recipe_input_options_fluid ON recipe_input_options(fluid_id)");
        st.executeUpdate("CREATE INDEX idx_recipe_input_options_ore ON recipe_input_options(ore_name)");
        st.executeUpdate("CREATE INDEX idx_recipe_outputs_recipe ON recipe_outputs(recipe_id)");
        st.executeUpdate("CREATE INDEX idx_recipe_outputs_item ON recipe_outputs(item_id)");
        st.executeUpdate("CREATE INDEX idx_recipe_outputs_fluid ON recipe_outputs(fluid_id)");
        st.executeUpdate("CREATE INDEX idx_recipe_edges_resource ON recipe_edges(direction, resource_key)");
        st.executeUpdate("CREATE INDEX idx_recipe_edges_recipe ON recipe_edges(recipe_id, direction, position, option_index)");
        st.executeUpdate("CREATE UNIQUE INDEX idx_recipe_edges_unique_position ON recipe_edges(recipe_id, direction, position, COALESCE(option_index, -1))");
        st.executeUpdate("CREATE INDEX idx_machine_options_capability ON machine_options(capability_id)");
        st.executeUpdate("CREATE INDEX idx_ore_materials_internal_name ON ore_materials(internal_name COLLATE NOCASE)");
        st.executeUpdate("CREATE INDEX idx_ore_materials_localized_name ON ore_materials(localized_name COLLATE NOCASE)");
        st.executeUpdate("CREATE INDEX idx_ore_veins_name ON ore_veins(display_name COLLATE NOCASE)");
        st.executeUpdate("CREATE INDEX idx_ore_veins_key ON ore_veins(vein_key)");
        st.executeUpdate("CREATE INDEX idx_ore_vein_materials_material ON ore_vein_materials(material_id, vein_id)");
        st.executeUpdate("CREATE INDEX idx_ore_vein_dimensions_key ON ore_vein_dimensions(dimension_key, vein_id)");
        st.executeUpdate("CREATE INDEX idx_ore_vein_dimensions_dimension ON ore_vein_dimensions(dimension_name COLLATE NOCASE, vein_id)");
        st.executeUpdate("CREATE INDEX idx_small_ores_material ON small_ores(material_id)");
        st.executeUpdate("CREATE INDEX idx_small_ores_key ON small_ores(small_ore_key)");
        st.executeUpdate("CREATE INDEX idx_small_ore_dimensions_key ON small_ore_dimensions(dimension_key, small_ore_id)");
        st.executeUpdate("CREATE INDEX idx_small_ore_dimensions_dimension ON small_ore_dimensions(dimension_name COLLATE NOCASE, small_ore_id)");
      } finally {
        st.close();
      }

      putManifest = conn.prepareStatement("INSERT OR REPLACE INTO manifest(key, value) VALUES (?, ?)");
      insertItem =
          conn.prepareStatement(
              "INSERT OR IGNORE INTO items(registry_name, damage, display_name, unlocalized_name, max_damage) VALUES (?, ?, ?, ?, ?)");
      selectItem = conn.prepareStatement("SELECT id FROM items WHERE registry_name = ? AND damage = ?");
      insertFluid = conn.prepareStatement("INSERT OR IGNORE INTO fluids(fluid_name, localized_name) VALUES (?, ?)");
      selectFluid = conn.prepareStatement("SELECT id FROM fluids WHERE fluid_name = ?");
      insertHandler = conn.prepareStatement("INSERT OR IGNORE INTO recipe_handlers(name) VALUES (?)");
      selectHandler = conn.prepareStatement("SELECT id FROM recipe_handlers WHERE name = ?");
      insertRecipe =
          conn.prepareStatement(
              "INSERT INTO recipes(handler_id, recipe_key, category, duration_ticks, eut, special_value, hidden, fake, enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
              Statement.RETURN_GENERATED_KEYS);
      insertInput =
          conn.prepareStatement(
              "INSERT INTO recipe_inputs(recipe_id, position, kind, amount, label) VALUES (?, ?, ?, ?, ?)",
              Statement.RETURN_GENERATED_KEYS);
      insertInputOption =
          conn.prepareStatement(
              "INSERT INTO recipe_input_options(input_id, option_index, kind, item_id, fluid_id, amount, ore_name, label) VALUES (?, ?, ?, ?, ?, ?, ?, ?)");
      insertOutput =
          conn.prepareStatement(
              "INSERT INTO recipe_outputs(recipe_id, position, kind, amount, item_id, fluid_id, chance, is_primary, label) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)");
      updateOutputChance = conn.prepareStatement("UPDATE recipe_outputs SET chance = ? WHERE recipe_id = ? AND position = ? AND kind = 'item'");
      insertMetadata = conn.prepareStatement("INSERT INTO recipe_metadata(recipe_id, key, value) VALUES (?, ?, ?)");
      insertDumpError = conn.prepareStatement("INSERT INTO dump_errors(category, recipe_key, handler_name, message) VALUES (?, ?, ?, ?)");
      insertOreMaterial = conn.prepareStatement("INSERT OR IGNORE INTO ore_materials(material_key, internal_name, localized_name) VALUES (?, ?, ?)");
      selectOreMaterial = conn.prepareStatement("SELECT id FROM ore_materials WHERE material_key = ?");
      insertOreVein = conn.prepareStatement("INSERT INTO ore_veins(vein_key, internal_name, display_name, min_y, max_y, weight, density, size) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", Statement.RETURN_GENERATED_KEYS);
      insertOreVeinMaterial = conn.prepareStatement("INSERT INTO ore_vein_materials(vein_id, role, material_id) VALUES (?, ?, ?)");
      insertOreVeinDimension = conn.prepareStatement("INSERT INTO ore_vein_dimensions(vein_id, dimension_key, dimension_name, min_y, max_y) VALUES (?, ?, ?, ?, ?)");
      insertSmallOre = conn.prepareStatement("INSERT INTO small_ores(small_ore_key, internal_name, material_id, min_y, max_y, amount_per_chunk) VALUES (?, ?, ?, ?, ?, ?)", Statement.RETURN_GENERATED_KEYS);
      insertSmallOreDimension = conn.prepareStatement("INSERT INTO small_ore_dimensions(small_ore_id, dimension_key, dimension_name) VALUES (?, ?, ?)");
    }

    private void finishSchema() throws SQLException {
      Statement st = conn.createStatement();
      try {
        st.executeUpdate("UPDATE recipes SET valid=CASE WHEN EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=recipes.id) AND EXISTS (SELECT 1 FROM recipe_outputs ro WHERE ro.recipe_id=recipes.id) THEN 1 ELSE 0 END");
        st.executeUpdate(
            "INSERT OR IGNORE INTO machine_capabilities(handler_id, capability_key, machine_name_hint) SELECT id, lower(replace(replace(replace(name, 'gt.recipe.', ''), '.', '_'), ' ', '_')), replace(replace(name, 'gt.recipe.', ''), '_', ' ') FROM recipe_handlers");
        st.executeUpdate(
            "INSERT INTO recipe_edges(recipe_id, direction, position, option_index, resource_kind, resource_key, resource_name, item_id, fluid_id, ore_name, amount, chance, expected_amount, is_primary, consumed, catalyst) SELECT ro.recipe_id, 'output', ro.position, NULL, ro.kind, CASE WHEN ro.item_id IS NOT NULL THEN 'item:' || i.registry_name || ':' || i.damage WHEN ro.fluid_id IS NOT NULL THEN 'fluid:' || f.fluid_name ELSE ro.kind || ':' || COALESCE(ro.label, '') END, COALESCE(i.display_name, f.localized_name, f.fluid_name, ro.label), ro.item_id, ro.fluid_id, NULL, ro.amount, ro.chance, CAST(ro.amount AS REAL) * ro.chance / 10000.0, ro.is_primary, 0, 0 FROM recipe_outputs ro LEFT JOIN items i ON i.id=ro.item_id LEFT JOIN fluids f ON f.id=ro.fluid_id");
        st.executeUpdate(
            "INSERT INTO recipe_edges(recipe_id, direction, position, option_index, resource_kind, resource_key, resource_name, item_id, fluid_id, ore_name, amount, chance, expected_amount, is_primary, consumed, catalyst) SELECT ri.recipe_id, 'input', ri.position, rio.option_index, COALESCE(rio.kind, ri.kind), CASE WHEN rio.item_id IS NOT NULL THEN 'item:' || i.registry_name || ':' || i.damage WHEN rio.fluid_id IS NOT NULL THEN 'fluid:' || f.fluid_name WHEN rio.ore_name IS NOT NULL THEN 'oredict:' || rio.ore_name ELSE COALESCE(rio.kind, ri.kind) || ':' || COALESCE(rio.label, ri.label, '') END, COALESCE(i.display_name, f.localized_name, f.fluid_name, rio.ore_name, rio.label, ri.label), rio.item_id, rio.fluid_id, rio.ore_name, COALESCE(rio.amount, ri.amount), 10000, COALESCE(rio.amount, ri.amount), 0, ri.consumed, ri.catalyst FROM recipe_inputs ri LEFT JOIN recipe_input_options rio ON rio.input_id=ri.id LEFT JOIN items i ON i.id=rio.item_id LEFT JOIN fluids f ON f.id=rio.fluid_id");
        st.executeUpdate(
            "CREATE VIEW resource_catalog AS SELECT 'item' AS resource_kind, id AS resource_id, 'item:' || registry_name || ':' || damage AS resource_key, display_name AS resource_name, registry_name, damage, NULL AS fluid_name FROM items UNION ALL SELECT 'fluid', id, 'fluid:' || fluid_name, COALESCE(localized_name, fluid_name), NULL, NULL, fluid_name FROM fluids");
        st.executeUpdate(
            "CREATE VIEW recipe_routes AS SELECT r.id AS recipe_id, r.recipe_key, r.category, h.name AS handler_name, mc.capability_key, mc.machine_name_hint, r.duration_ticks, r.eut, CASE WHEN r.eut IS NULL THEN NULL WHEN abs(r.eut)<=8 THEN 'ULV' WHEN abs(r.eut)<=32 THEN 'LV' WHEN abs(r.eut)<=128 THEN 'MV' WHEN abs(r.eut)<=512 THEN 'HV' WHEN abs(r.eut)<=2048 THEN 'EV' WHEN abs(r.eut)<=8192 THEN 'IV' WHEN abs(r.eut)<=32768 THEN 'LuV' WHEN abs(r.eut)<=131072 THEN 'ZPM' WHEN abs(r.eut)<=524288 THEN 'UV' ELSE 'UHV+' END AS voltage_tier, e.position AS output_position, e.resource_kind AS output_kind, e.resource_key AS output_resource_key, e.resource_name AS output_name, i.registry_name, i.damage, f.fluid_name, e.amount AS output_amount, e.chance, e.expected_amount AS expected_output_amount, e.is_primary FROM recipe_edges e JOIN recipes r ON r.id=e.recipe_id JOIN recipe_handlers h ON h.id=r.handler_id LEFT JOIN machine_capabilities mc ON mc.handler_id=h.id LEFT JOIN items i ON i.id=e.item_id LEFT JOIN fluids f ON f.id=e.fluid_id WHERE e.direction='output' AND r.valid=1 AND r.hidden=0 AND r.fake=0 AND r.enabled=1");
        st.executeUpdate(
            "CREATE VIEW recipe_ingredients AS SELECT e.recipe_id, e.position AS input_position, e.option_index, e.resource_kind AS input_kind, e.resource_key AS input_resource_key, e.resource_name AS input_name, i.registry_name, i.damage, f.fluid_name, e.ore_name, e.amount AS input_amount, e.consumed, e.catalyst FROM recipe_edges e LEFT JOIN items i ON i.id=e.item_id LEFT JOIN fluids f ON f.id=e.fluid_id WHERE e.direction='input'");
        st.executeUpdate(
            "CREATE VIEW handler_machine_options AS SELECT mc.id AS capability_id, mc.handler_id, h.name AS handler_name, mc.capability_key, mc.machine_name_hint, mo.item_id, i.registry_name AS item_registry_name, i.damage AS item_damage, mo.block_registry_name, mo.block_meta, mo.display_name, mo.tier_name, mo.min_eut, mo.max_eut, mo.source FROM machine_capabilities mc JOIN recipe_handlers h ON h.id=mc.handler_id LEFT JOIN machine_options mo ON mo.capability_id=mc.id LEFT JOIN items i ON i.id=mo.item_id");
        st.executeUpdate(
            "CREATE VIEW recipe_data_quality AS SELECT r.id AS recipe_id, r.recipe_key, h.name AS handler_name, r.category, r.valid, r.hidden, r.fake, r.enabled, (SELECT count(*) FROM recipe_inputs ri WHERE ri.recipe_id=r.id) AS input_count, (SELECT count(*) FROM recipe_outputs ro WHERE ro.recipe_id=r.id) AS output_count, (SELECT count(*) FROM recipe_inputs ri LEFT JOIN recipe_input_options rio ON rio.input_id=ri.id WHERE ri.recipe_id=r.id AND rio.id IS NULL) AS optionless_input_count FROM recipes r JOIN recipe_handlers h ON h.id=r.handler_id");
        st.executeUpdate(
            "CREATE VIEW ore_generation_routes AS SELECT 'vein' AS generation_kind, v.vein_key AS source_key, v.display_name AS source_name, m.material_key, COALESCE(m.localized_name, m.internal_name) AS material_name, vm.role, d.dimension_key, d.dimension_name, d.min_y, d.max_y, v.weight, v.density, v.size, NULL AS amount_per_chunk FROM ore_veins v JOIN ore_vein_materials vm ON vm.vein_id=v.id JOIN ore_materials m ON m.id=vm.material_id JOIN ore_vein_dimensions d ON d.vein_id=v.id UNION ALL SELECT 'small_ore', s.small_ore_key, s.internal_name, m.material_key, COALESCE(m.localized_name, m.internal_name), 'small', d.dimension_key, d.dimension_name, s.min_y, s.max_y, NULL, NULL, NULL, s.amount_per_chunk FROM small_ores s JOIN ore_materials m ON m.id=s.material_id JOIN small_ore_dimensions d ON d.small_ore_id=s.id");
        st.executeUpdate("CREATE VIRTUAL TABLE item_search USING fts5(display_name, registry_name, unlocalized_name, content='items', content_rowid='id')");
        st.executeUpdate("INSERT INTO item_search(rowid, display_name, registry_name, unlocalized_name) SELECT id, display_name, registry_name, unlocalized_name FROM items");
      } finally {
        st.close();
      }
    }

    private Savepoint beginRecipe() throws SQLException {
      return conn.setSavepoint();
    }

    private void releaseRecipe(Savepoint savepoint) throws SQLException {
      if (savepoint != null) {
        conn.releaseSavepoint(savepoint);
      }
    }

    private void rollbackRecipe(Savepoint savepoint) {
      if (savepoint == null) {
        return;
      }
      try {
        conn.rollback(savepoint);
        conn.releaseSavepoint(savepoint);
        itemIds.clear();
        fluidIds.clear();
        handlerIds.clear();
        oreMaterialIds.clear();
      } catch (SQLException ignored) {
      }
    }

    private void putManifest(String key, String value) throws SQLException {
      putManifest.setString(1, key);
      putManifest.setString(2, value);
      putManifest.executeUpdate();
    }

    private int itemId(Object stack) throws Exception {
      String registryName = accessors.registryName(stack);
      int damage = accessors.damage(stack);
      String key = registryName + "#" + damage;
      Integer cached = itemIds.get(key);
      if (cached != null) {
        return cached.intValue();
      }

      insertItem.setString(1, registryName);
      insertItem.setInt(2, damage);
      insertItem.setString(3, accessors.displayName(stack));
      insertItem.setString(4, accessors.unlocalizedName(stack));
      setNullableInt(insertItem, 5, accessors.maxDamage(stack));
      insertItem.executeUpdate();

      selectItem.setString(1, registryName);
      selectItem.setInt(2, damage);
      int id = selectSingleId(selectItem, "item " + key);
      itemIds.put(key, Integer.valueOf(id));
      return id;
    }

    private int fluidId(Object fluidStack) throws Exception {
      String name = fluidName(fluidStack);
      Integer cached = fluidIds.get(name);
      if (cached != null) {
        return cached.intValue();
      }
      insertFluid.setString(1, name);
      insertFluid.setString(2, fluidLocalizedName(fluidStack));
      insertFluid.executeUpdate();

      selectFluid.setString(1, name);
      int id = selectSingleId(selectFluid, "fluid " + name);
      fluidIds.put(name, Integer.valueOf(id));
      return id;
    }

    private int handlerId(String name) throws SQLException {
      Integer cached = handlerIds.get(name);
      if (cached != null) {
        return cached.intValue();
      }
      insertHandler.setString(1, name);
      insertHandler.executeUpdate();
      selectHandler.setString(1, name);
      int id = selectSingleId(selectHandler, "recipe handler " + name);
      handlerIds.put(name, Integer.valueOf(id));
      return id;
    }

    private int insertRecipe(
        String handlerName,
        String recipeKey,
        String category,
        Integer durationTicks,
        Integer eut,
        Integer specialValue,
        boolean hidden,
        boolean fake,
        boolean enabled)
        throws SQLException {
      insertRecipe.setInt(1, handlerId(handlerName));
      insertRecipe.setString(2, recipeKey);
      insertRecipe.setString(3, category);
      setNullableInt(insertRecipe, 4, durationTicks);
      setNullableInt(insertRecipe, 5, eut);
      setNullableInt(insertRecipe, 6, specialValue);
      insertRecipe.setInt(7, hidden ? 1 : 0);
      insertRecipe.setInt(8, fake ? 1 : 0);
      insertRecipe.setInt(9, enabled ? 1 : 0);
      insertRecipe.executeUpdate();
      return generatedId(insertRecipe);
    }

    private int insertInput(int recipeId, int position, String kind, Integer amount, String label) throws SQLException {
      insertInput.setInt(1, recipeId);
      insertInput.setInt(2, position);
      insertInput.setString(3, kind);
      setNullableInt(insertInput, 4, amount);
      insertInput.setString(5, label);
      insertInput.executeUpdate();
      return generatedId(insertInput);
    }

    private void insertInputOption(
        int inputId,
        int optionIndex,
        String kind,
        Integer itemId,
        Integer fluidId,
        Integer chance,
        String oreName,
        String label,
        Integer amount)
        throws SQLException {
      insertInputOption.setInt(1, inputId);
      insertInputOption.setInt(2, optionIndex);
      insertInputOption.setString(3, kind);
      setNullableInt(insertInputOption, 4, itemId);
      setNullableInt(insertInputOption, 5, fluidId);
      setNullableInt(insertInputOption, 6, amount);
      insertInputOption.setString(7, oreName);
      insertInputOption.setString(8, label);
      insertInputOption.executeUpdate();
    }

    private void insertOutput(
        int recipeId,
        int position,
        String kind,
        Integer amount,
        Integer itemId,
        Integer fluidId,
        Integer chance,
        String label)
        throws SQLException {
      insertOutput.setInt(1, recipeId);
      insertOutput.setInt(2, position);
      insertOutput.setString(3, kind);
      setNullableInt(insertOutput, 4, amount);
      setNullableInt(insertOutput, 5, itemId);
      setNullableInt(insertOutput, 6, fluidId);
      setNullableInt(insertOutput, 7, chance);
      insertOutput.setInt(8, position == 0 ? 1 : 0);
      insertOutput.setString(9, label);
      insertOutput.executeUpdate();
    }

    private void updateOutputChance(int recipeId, int position, Integer chance) throws SQLException {
      setNullableInt(updateOutputChance, 1, chance);
      updateOutputChance.setInt(2, recipeId);
      updateOutputChance.setInt(3, position);
      updateOutputChance.executeUpdate();
    }

    private void insertMetadata(int recipeId, String key, String value) throws SQLException {
      insertMetadata.setInt(1, recipeId);
      insertMetadata.setString(2, key);
      insertMetadata.setString(3, value);
      insertMetadata.executeUpdate();
    }

    private void insertDumpError(String category, String recipeKey, String handlerName, String message) throws SQLException {
      insertDumpError.setString(1, category);
      insertDumpError.setString(2, recipeKey);
      insertDumpError.setString(3, handlerName);
      insertDumpError.setString(4, message != null ? message : "unknown dump error");
      insertDumpError.executeUpdate();
    }

    private void recordDumpError(String category, String recipeKey, String handlerName, String message) {
      try {
        insertDumpError(category, recipeKey, handlerName, message);
      } catch (SQLException error) {
        appendLog("[GREGGPT] failed to record dump error: " + error);
      }
    }

    private int oreMaterialId(Object material) throws Exception {
      if (material == null) {
        throw new SQLException("worldgen material is null");
      }
      String internalName = String.valueOf(invokeAny(material, "getInternalName"));
      String materialKey = "material:" + normalizedWorldgenKey(internalName);
      Integer cached = oreMaterialIds.get(materialKey);
      if (cached != null) {
        return cached.intValue();
      }
      String localizedName;
      try {
        localizedName = String.valueOf(invokeAny(material, "getLocalizedName"));
      } catch (Throwable ignored) {
        localizedName = String.valueOf(invokeAny(material, "getDefaultLocalName"));
      }
      insertOreMaterial.setString(1, materialKey);
      insertOreMaterial.setString(2, internalName);
      insertOreMaterial.setString(3, localizedName);
      insertOreMaterial.executeUpdate();
      selectOreMaterial.setString(1, materialKey);
      int id = selectSingleId(selectOreMaterial, "ore material " + materialKey);
      oreMaterialIds.put(materialKey, Integer.valueOf(id));
      return id;
    }

    private int insertOreVein(String key, String internalName, String displayName, int minY, int maxY, int weight, int density, int size) throws SQLException {
      insertOreVein.setString(1, key);
      insertOreVein.setString(2, internalName);
      insertOreVein.setString(3, displayName);
      insertOreVein.setInt(4, minY);
      insertOreVein.setInt(5, maxY);
      insertOreVein.setInt(6, weight);
      insertOreVein.setInt(7, density);
      insertOreVein.setInt(8, size);
      insertOreVein.executeUpdate();
      return generatedId(insertOreVein);
    }

    private void insertOreVeinMaterial(int veinId, String role, Object material) throws Exception {
      if (material == null) {
        return;
      }
      insertOreVeinMaterial.setInt(1, veinId);
      insertOreVeinMaterial.setString(2, role);
      insertOreVeinMaterial.setInt(3, oreMaterialId(material));
      insertOreVeinMaterial.executeUpdate();
    }

    private void insertOreVeinDimension(int veinId, String dimensionKey, String dimensionName, int minY, int maxY) throws SQLException {
      insertOreVeinDimension.setInt(1, veinId);
      insertOreVeinDimension.setString(2, dimensionKey);
      insertOreVeinDimension.setString(3, dimensionName);
      insertOreVeinDimension.setInt(4, minY);
      insertOreVeinDimension.setInt(5, maxY);
      insertOreVeinDimension.executeUpdate();
    }

    private int insertSmallOre(String key, String internalName, Object material, int minY, int maxY, int amount) throws Exception {
      insertSmallOre.setString(1, key);
      insertSmallOre.setString(2, internalName);
      insertSmallOre.setInt(3, oreMaterialId(material));
      insertSmallOre.setInt(4, minY);
      insertSmallOre.setInt(5, maxY);
      insertSmallOre.setInt(6, amount);
      insertSmallOre.executeUpdate();
      return generatedId(insertSmallOre);
    }

    private void insertSmallOreDimension(int smallOreId, String dimensionKey, String dimensionName) throws SQLException {
      insertSmallOreDimension.setInt(1, smallOreId);
      insertSmallOreDimension.setString(2, dimensionKey);
      insertSmallOreDimension.setString(3, dimensionName);
      insertSmallOreDimension.executeUpdate();
    }
  }

  private static final class Accessors {
    private Method stackGetItem;
    private Method stackGetItemDamage;
    private Method stackGetDisplayName;
    private Method stackGetUnlocalizedName;
    private Method stackGetMaxDamage;
    private Method uniqueIdentifierLookup;
    private Field uniqueIdentifierModId;
    private Field uniqueIdentifierName;
    private Field stackSizeField;

    private int stackSize(Object stack) {
      try {
        if (stackSizeField == null) {
          stackSizeField = findField(stack.getClass(), "stackSize", "field_77994_a");
        }
        return stackSizeField.getInt(stack);
      } catch (Throwable ignored) {
        return 1;
      }
    }

    private int damage(Object stack) {
      try {
        if (stackGetItemDamage == null) {
          stackGetItemDamage = findMethod(stack.getClass(), "getItemDamage", "func_77960_j");
        }
        return ((Integer) stackGetItemDamage.invoke(stack)).intValue();
      } catch (Exception e) {
        return 0;
      }
    }

    private String displayName(Object stack) {
      try {
        if (stackGetDisplayName == null) {
          stackGetDisplayName = findMethod(stack.getClass(), "getDisplayName", "func_82833_r");
        }
        Object value = stackGetDisplayName.invoke(stack);
        return value != null ? value.toString() : "";
      } catch (Throwable ignored) {
        return "";
      }
    }

    private String unlocalizedName(Object stack) {
      try {
        if (stackGetUnlocalizedName == null) {
          stackGetUnlocalizedName = findMethod(stack.getClass(), "getUnlocalizedName", "func_77977_a");
        }
        Object value = stackGetUnlocalizedName.invoke(stack);
        return value != null ? value.toString() : "";
      } catch (Throwable ignored) {
        return "";
      }
    }

    private Integer maxDamage(Object stack) {
      try {
        if (stackGetMaxDamage == null) {
          stackGetMaxDamage = findMethod(stack.getClass(), "getMaxDamage", "func_77958_k");
        }
        return (Integer) stackGetMaxDamage.invoke(stack);
      } catch (Throwable ignored) {
        return null;
      }
    }

    private String registryName(Object stack) throws Exception {
      Object item = item(stack);
      if (item == null) {
        return "";
      }
      if (uniqueIdentifierLookup == null) {
        uniqueIdentifierLookup = findMethod(GameRegistry.class, "findUniqueIdentifierFor", item.getClass());
      }
      Object identifier = uniqueIdentifierLookup.invoke(null, item);
      if (identifier == null) {
        return "";
      }
      if (uniqueIdentifierModId == null) {
        uniqueIdentifierModId = findField(identifier.getClass(), "modId");
      }
      if (uniqueIdentifierName == null) {
        uniqueIdentifierName = findField(identifier.getClass(), "name");
      }
      Object modId = uniqueIdentifierModId.get(identifier);
      Object name = uniqueIdentifierName.get(identifier);
      if (modId == null || name == null) {
        return "";
      }
      return modId.toString() + ":" + name.toString();
    }

    private Object item(Object stack) throws Exception {
      if (stackGetItem == null) {
        stackGetItem = findMethod(stack.getClass(), "getItem", "func_77973_b");
      }
      return stackGetItem.invoke(stack);
    }
  }

  private static boolean isItemStack(Object value) {
    return value != null && "net.minecraft.item.ItemStack".equals(value.getClass().getName());
  }

  private static boolean isEmptyStack(Object value) {
    if (!isItemStack(value)) {
      return value == null;
    }
    try {
      return new Accessors().item(value) == null;
    } catch (Throwable ignored) {
      return true;
    }
  }

  private static int fluidAmount(Object fluidStack) {
    Object amount = fieldValue(fluidStack, "amount");
    if (amount instanceof Number) {
      return ((Number) amount).intValue();
    }
    return 0;
  }

  private static String fluidName(Object fluidStack) throws Exception {
    Object fluid = invokeAny(fluidStack, "getFluid");
    if (fluid == null) {
      return "";
    }
    Object name = invokeAny(fluid, "getName");
    return name != null ? name.toString() : "";
  }

  private static String fluidLocalizedName(Object fluidStack) {
    try {
      Object value = invokeAny(fluidStack, "getLocalizedName");
      return value != null ? value.toString() : "";
    } catch (Throwable ignored) {
      return "";
    }
  }

  private static Object invokeStatic(Class<?> type, String... names) throws Exception {
    Method method = findMethod(type, names);
    return method.invoke(null);
  }

  private static Object invokeAny(Object target, String... names) throws Exception {
    Method method = findMethod(target.getClass(), names);
    return method.invoke(target);
  }

  private static Object invokeOneArg(Object target, String name, Class<?> parameterType, Object value) throws Exception {
    Method method = findMethod(target.getClass(), name, parameterType);
    return method.invoke(target, value);
  }

  private static int intValue(Object value) {
    if (!(value instanceof Number)) {
      throw new IllegalArgumentException("expected number, got " + value);
    }
    return ((Number) value).intValue();
  }

  private static int numberField(Object target, String name) {
    Integer value = intField(target, name);
    if (value == null) {
      throw new IllegalArgumentException("missing numeric field " + name + " on " + target.getClass().getName());
    }
    return value.intValue();
  }

  private static List<String> stringValues(Object values) {
    List<String> out = new ArrayList<String>();
    for (Object value : iterable(values)) {
      if (value != null && !value.toString().trim().isEmpty()) {
        out.add(value.toString().trim());
      }
    }
    Collections.sort(out);
    return out;
  }

  private static String normalizedWorldgenKey(String value) {
    String lower = value == null ? "" : value.trim().toLowerCase(Locale.ROOT);
    StringBuilder out = new StringBuilder(lower.length());
    boolean separator = false;
    for (int i = 0; i < lower.length(); i++) {
      char ch = lower.charAt(i);
      if ((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
        if (separator && out.length() > 0) {
          out.append('_');
        }
        out.append(ch);
        separator = false;
      } else {
        separator = true;
      }
    }
    return out.toString();
  }

  private static String friendlyWorldgenName(String value) {
    if (value == null || value.trim().isEmpty()) {
      return "Unknown";
    }
    if ("MakeMake".equals(value.trim())) {
      return "MakeMake";
    }
    String source = value.trim().replace('_', ' ');
    StringBuilder out = new StringBuilder(source.length() + 8);
    for (int i = 0; i < source.length(); i++) {
      char ch = source.charAt(i);
      if (i > 0 && Character.isUpperCase(ch) && Character.isLowerCase(source.charAt(i - 1))) {
        out.append(' ');
      }
      out.append(ch);
    }
    return out.toString();
  }

  private static Object firstFieldValue(Object target, String... names) {
    for (int i = 0; i < names.length; i++) {
      Object value = fieldValue(target, names[i]);
      if (value != null) {
        return value;
      }
    }
    return null;
  }

  private static Object fieldValue(Object target, String name) {
    if (target == null) {
      return null;
    }
    try {
      Field field = findField(target.getClass(), name);
      return field.get(target);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static Integer intField(Object target, String name) {
    Object value = fieldValue(target, name);
    if (value instanceof Number) {
      return Integer.valueOf(((Number) value).intValue());
    }
    return null;
  }

  private static Boolean boolField(Object target, String... names) {
    for (int i = 0; i < names.length; i++) {
      Object value = fieldValue(target, names[i]);
      if (value instanceof Boolean) {
        return (Boolean) value;
      }
    }
    return null;
  }

  private static boolean booleanFieldTrue(Object target, String name) {
    Boolean value = boolField(target, name);
    return value != null && value.booleanValue();
  }

  private static List<Object> iterable(Object value) {
    if (value == null) {
      return Collections.emptyList();
    }
    if (value instanceof Collection) {
      return new ArrayList<Object>((Collection<?>) value);
    }
    if (value instanceof Map) {
      return new ArrayList<Object>(((Map<?, ?>) value).values());
    }
    Class<?> type = value.getClass();
    if (type.isArray()) {
      int length = Array.getLength(value);
      List<Object> out = new ArrayList<Object>(length);
      for (int i = 0; i < length; i++) {
        out.add(Array.get(value, i));
      }
      return out;
    }
    List<Object> single = new ArrayList<Object>(1);
    single.add(value);
    return single;
  }

  private static List<Object> snapshotMapEntries(Map<?, ?> map) {
    ConcurrentModificationException last = null;
    for (int attempt = 0; attempt < 5; attempt++) {
      try {
        return new ArrayList<Object>(map.entrySet());
      } catch (ConcurrentModificationException concurrentWrite) {
        last = concurrentWrite;
        sleepQuietly(100L);
      }
    }
    throw last != null ? last : new ConcurrentModificationException("map changed while taking snapshot");
  }

  private static Method findMethod(Class<?> type, String... names) {
    Class<?> current = type;
    while (current != null) {
      for (int i = 0; i < names.length; i++) {
        try {
          Method method = current.getDeclaredMethod(names[i]);
          method.setAccessible(true);
          return method;
        } catch (NoSuchMethodException ignored) {
        }
      }
      current = current.getSuperclass();
    }
    throw new RuntimeException("failed to find no-arg method on " + type);
  }

  private static Method findMethod(Class<?> type, String name, Class<?> parameterType) {
    Class<?> current = type;
    while (current != null) {
      Method[] methods = current.getDeclaredMethods();
      for (int i = 0; i < methods.length; i++) {
        Method method = methods[i];
        Class<?>[] parameterTypes = method.getParameterTypes();
        if (!method.getName().equals(name) || parameterTypes.length != 1) {
          continue;
        }
        if (!parameterTypes[0].isAssignableFrom(parameterType)) {
          continue;
        }
        method.setAccessible(true);
        return method;
      }
      current = current.getSuperclass();
    }
    throw new RuntimeException("failed to find method " + name + "(" + parameterType.getName() + ") on " + type);
  }

  private static Field findField(Class<?> type, String... names) {
    Class<?> current = type;
    while (current != null) {
      for (int i = 0; i < names.length; i++) {
        try {
          Field field = current.getDeclaredField(names[i]);
          field.setAccessible(true);
          return field;
        } catch (NoSuchFieldException ignored) {
        }
      }
      current = current.getSuperclass();
    }
    throw new RuntimeException("failed to find field on " + type);
  }

  private static Class<?> classForName(String name) throws ClassNotFoundException {
    ClassLoader loader = Thread.currentThread().getContextClassLoader();
    if (loader != null) {
      return Class.forName(name, false, loader);
    }
    return Class.forName(name);
  }

  private static void setNullableInt(PreparedStatement ps, int index, Integer value) throws SQLException {
    if (value == null) {
      ps.setNull(index, java.sql.Types.INTEGER);
    } else {
      ps.setInt(index, value.intValue());
    }
  }

  private static int generatedId(PreparedStatement ps) throws SQLException {
    ResultSet rs = ps.getGeneratedKeys();
    try {
      if (!rs.next()) {
        throw new SQLException("insert did not return a generated id");
      }
      return rs.getInt(1);
    } finally {
      rs.close();
    }
  }

  private static int selectSingleId(PreparedStatement ps, String label) throws SQLException {
    ResultSet rs = ps.executeQuery();
    try {
      if (!rs.next()) {
        throw new SQLException("failed to select " + label);
      }
      return rs.getInt(1);
    } finally {
      rs.close();
    }
  }

  private static void rollbackQuietly(Connection conn) {
    try {
      conn.rollback();
    } catch (Throwable ignored) {
    }
  }

  private static File getDumpsDir() {
    File configDir = Loader.instance().getConfigDir();
    File baseDir = configDir != null ? configDir.getParentFile() : new File(".");
    File dumpsDir = new File(baseDir, "dumps");
    if (!dumpsDir.exists() && !dumpsDir.mkdirs()) {
      throw new RuntimeException("failed to create dump dir: " + dumpsDir);
    }
    return dumpsDir;
  }

  private static void appendLog(String message) {
    String line = message != null ? message : "";
    System.out.println(line);
    try {
      File logFile = new File(getDumpsDir(), LOG_FILE_NAME);
      BufferedWriter writer =
          java.nio.file.Files.newBufferedWriter(
              logFile.toPath(),
              StandardCharsets.UTF_8,
              java.nio.file.StandardOpenOption.CREATE,
              java.nio.file.StandardOpenOption.APPEND);
      try {
        writer.write(line);
        writer.newLine();
      } finally {
        writer.close();
      }
    } catch (IOException ignored) {
    }
  }

  private static void appendThrowable(String prefix, Throwable throwable) {
    appendLog(prefix + ": " + throwable);
    Throwable current = throwable;
    while (current != null) {
      StackTraceElement[] stack = current.getStackTrace();
      int limit = Math.min(stack.length, 12);
      for (int i = 0; i < limit; i++) {
        appendLog("[GREGGPT]   at " + stack[i]);
      }
      current = current.getCause();
      if (current != null) {
        appendLog("[GREGGPT] caused by " + current);
      }
    }
  }

  private static String throwableSummary(Throwable throwable) {
    if (throwable == null) {
      return "unknown dump error";
    }
    Throwable current = throwable;
    int depth = 0;
    while (current.getCause() != null && current.getCause() != current && depth < 32) {
      current = current.getCause();
      depth++;
    }
    return current.toString();
  }

  private static void sleepQuietly(long millis) {
    try {
      Thread.sleep(millis);
    } catch (InterruptedException ignored) {
    }
  }
}
