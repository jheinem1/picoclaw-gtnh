package picoclaw.oredict;

import cpw.mods.fml.common.Loader;
import cpw.mods.fml.common.LoaderState;
import cpw.mods.fml.common.Mod;
import cpw.mods.fml.common.Mod.EventHandler;
import cpw.mods.fml.common.event.FMLLoadCompleteEvent;
import cpw.mods.fml.common.registry.GameRegistry;
import java.io.BufferedWriter;
import java.io.File;
import java.io.IOException;
import java.io.PrintWriter;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;
import net.minecraftforge.oredict.OreDictionary;

@Mod(
    modid = "picoclaw_oredict_dump",
    name = "PicoClaw OreDict Dump",
    version = "1.0.0",
    acceptableRemoteVersions = "*"
)
public final class PicoClawOreDictDumpMod {
  private static final AtomicBoolean WORKER_STARTED = new AtomicBoolean(false);
  private static final String DUMP_FILE_NAME = "picoclaw_oredict_dump.tsv";
  private static final String LOG_FILE_NAME = "picoclaw_oredict_dump.log";

  public PicoClawOreDictDumpMod() {
    startDumpWorker("constructor");
  }

  @EventHandler
  public void onLoadComplete(FMLLoadCompleteEvent event) {
    startDumpWorker("load-complete");
  }

  private static void writeDump(File outFile) throws IOException {
    String[] oreNames = OreDictionary.getOreNames();
    Arrays.sort(oreNames);
    Accessors accessors = new Accessors();

    PrintWriter writer = new PrintWriter(outFile, StandardCharsets.UTF_8.name());
    try {
      writer.println("ore_name\treg_name\tdamage\tdisplay_name");
      for (String oreName : oreNames) {
        List<?> stacks = OreDictionary.getOres(oreName);
        for (Object stack : stacks) {
          if (stack == null) {
            continue;
          }
          Object item = accessors.getItem(stack);
          if (item == null) {
            continue;
          }
          Object regNameObj = accessors.getRegistryName(item);
          String regName = regNameObj != null ? regNameObj.toString() : "";
          if (regName.isEmpty()) {
            continue;
          }
          String displayName;
          try {
            displayName = accessors.getDisplayName(stack);
          } catch (Throwable ignored) {
            displayName = "";
          }
          writer.print(sanitize(oreName));
          writer.print('\t');
          writer.print(sanitize(regName));
          writer.print('\t');
          writer.print(accessors.getDamage(stack));
          writer.print('\t');
          writer.println(sanitize(displayName));
        }
      }
    } finally {
      writer.close();
    }
  }

  private static void startDumpWorker(final String trigger) {
    if (!WORKER_STARTED.compareAndSet(false, true)) {
      appendLog("[PICOCLAW] dump worker already started; ignoring trigger " + trigger);
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
            "picoclaw-oredict-dump");
    worker.setDaemon(true);
    worker.start();
  }

  private static void runDumpWorker(String trigger) {
    File dumpsDir = getDumpsDir();
    File outFile = new File(dumpsDir, DUMP_FILE_NAME);
    appendLog("[PICOCLAW] dump worker started from " + trigger);

    for (int attempt = 1; attempt <= 300; attempt++) {
      Loader loader = Loader.instance();
      LoaderState state = loader != null ? loader.getLoaderState() : null;
      int oreNameCount = 0;
      try {
        oreNameCount = OreDictionary.getOreNames().length;
      } catch (Throwable t) {
        appendLog("[PICOCLAW] ore dict probe failed on attempt " + attempt + ": " + t);
      }

      appendLog(
          "[PICOCLAW] attempt "
              + attempt
              + " state="
              + (state != null ? state.name() : "null")
              + " oreNames="
              + oreNameCount);

      if (loader != null && loader.hasReachedState(LoaderState.AVAILABLE) && oreNameCount > 0) {
        try {
          writeDump(outFile);
          appendLog("[PICOCLAW] wrote ore dict dump to " + outFile.getAbsolutePath());
          System.out.println("[PICOCLAW] wrote ore dict dump to " + outFile.getAbsolutePath());
          sleepQuietly(1500L);
          System.exit(0);
          return;
        } catch (Throwable t) {
          appendThrowable("[PICOCLAW] failed to write ore dict dump", t);
          throw new RuntimeException("failed to write ore dict dump", t);
        }
      }

      sleepQuietly(1000L);
    }

    throw new RuntimeException("timed out waiting for ore dictionary to become available");
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
        appendLog("[PICOCLAW]   at " + stack[i]);
      }
      current = current.getCause();
      if (current != null) {
        appendLog("[PICOCLAW] caused by " + current);
      }
    }
  }

  private static void sleepQuietly(long millis) {
    try {
      Thread.sleep(millis);
    } catch (InterruptedException ignored) {
    }
  }

  private static String sanitize(String value) {
    if (value == null) {
      return "";
    }
    return value.replace('\t', ' ').replace('\r', ' ').replace('\n', ' ').trim();
  }

  private static final class Accessors {
    private Method stackGetItem;
    private Method stackGetItemDamage;
    private Method stackGetDisplayName;
    private Method uniqueIdentifierLookup;
    private Field uniqueIdentifierModId;
    private Field uniqueIdentifierName;

    private Accessors() {}

    private Object getItem(Object stack) {
      try {
        if (stackGetItem == null) {
          stackGetItem = findMethod(stack.getClass(), "getItem", "func_77973_b");
        }
        return stackGetItem.invoke(stack);
      } catch (Exception e) {
        throw new RuntimeException("failed to access item", e);
      }
    }

    private int getDamage(Object stack) {
      try {
        if (stackGetItemDamage == null) {
          stackGetItemDamage = findMethod(stack.getClass(), "getItemDamage", "func_77960_j");
        }
        return ((Integer) stackGetItemDamage.invoke(stack)).intValue();
      } catch (Exception e) {
        throw new RuntimeException("failed to access item damage", e);
      }
    }

    private String getDisplayName(Object stack) {
      try {
        if (stackGetDisplayName == null) {
          stackGetDisplayName = findMethod(stack.getClass(), "getDisplayName", "func_82833_r");
        }
        Object value = stackGetDisplayName.invoke(stack);
        return value != null ? value.toString() : "";
      } catch (Exception e) {
        throw new RuntimeException("failed to access display name", e);
      }
    }

    private Object getRegistryName(Object item) {
      try {
        if (uniqueIdentifierLookup == null) {
          uniqueIdentifierLookup =
              findMethod(GameRegistry.class, "findUniqueIdentifierFor", item.getClass());
        }
        Object identifier = uniqueIdentifierLookup.invoke(null, item);
        if (identifier == null) {
          return null;
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
          return null;
        }
        return modId.toString() + ":" + name.toString();
      } catch (Exception e) {
        throw new RuntimeException("failed to resolve registry name", e);
      }
    }

    private static Method findMethod(Class<?> type, String... names) {
      Class<?> current = type;
      while (current != null) {
        for (String name : names) {
          try {
            Method method = current.getDeclaredMethod(name);
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
        for (Method method : methods) {
          Class<?>[] parameterTypes = method.getParameterTypes();
          if (!method.getName().equals(name)) {
            continue;
          }
          if (parameterTypes.length != 1) {
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
      throw new RuntimeException(
          "failed to find method " + name + "(" + parameterType.getName() + ") on " + type);
    }

    private static Field findField(Class<?> type, String name) {
      Class<?> current = type;
      while (current != null) {
        try {
          Field field = current.getDeclaredField(name);
          field.setAccessible(true);
          return field;
        } catch (NoSuchFieldException ignored) {
        }
        current = current.getSuperclass();
      }
      throw new RuntimeException("failed to find field " + name + " on " + type);
    }
  }
}
