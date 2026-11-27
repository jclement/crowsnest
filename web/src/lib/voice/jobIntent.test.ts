import { describe, expect, it } from "vitest";
import { interpretJob, readback } from "./jobIntent";

const projects = [
  { slug: "website", name: "Website" },
  { slug: "mobile", name: "Mobile app" },
  { slug: "crowsnest", name: "Crowsnest" },
];

describe("interpretJob", () => {
  it("finds the project named and takes it off the front of the brief", () => {
    expect(interpretJob("Website: fix the checkout validation", projects)).toEqual({ brief: "Fix the checkout validation", slug: "website", ambiguous: false, candidates: ["website"] });
    expect(interpretJob("for mobile app, bump the iOS target", projects).slug).toBe("mobile");
    expect(interpretJob("for mobile app, bump the iOS target", projects).brief).toBe("Bump the iOS target");
  });

  it("keeps a name used inside the sentence", () => {
    expect(interpretJob("make the crowsnest logo smaller", projects)).toMatchObject({ brief: "make the crowsnest logo smaller", slug: "crowsnest", ambiguous: false });
  });

  it("asks when two projects are named, or none and none is chosen", () => {
    expect(interpretJob("copy the website footer into the mobile app", projects)).toMatchObject({ slug: null, ambiguous: true, candidates: ["website", "mobile"] });
    expect(interpretJob("tidy the readme", projects)).toMatchObject({ slug: null, ambiguous: true });
  });

  it("a project chosen in the box stands, unless the speech names another (then it asks)", () => {
    expect(interpretJob("tidy the readme", projects, "website")).toMatchObject({ slug: "website", ambiguous: false });
    expect(interpretJob("tidy the readme", projects, "")).toMatchObject({ slug: null, ambiguous: false });
    expect(interpretJob("Crowsnest: tidy the readme", projects, "website")).toMatchObject({ ambiguous: true, candidates: ["website", "crowsnest"] });
  });
});

describe("readback", () => {
  it("says the destination and the job, and asks", () => {
    expect(readback("Fix the checkout validation.", "Website")).toBe("Website: Fix the checkout validation. Create the job?");
    expect(readback("tidy up", null)).toBe("The orchestrator: tidy up. Create the job?");
  });
});
