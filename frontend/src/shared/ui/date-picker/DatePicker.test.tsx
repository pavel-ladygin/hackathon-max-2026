import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DatePicker } from "./DatePicker";

describe("DatePicker", () => {
  it("opens a Russian calendar, applies a selected date, and returns focus", async () => {
    const onChange = vi.fn();
    render(<DatePicker id="from" label="С" value="2026-09-22" onChange={onChange} />);

    const trigger = screen.getByRole("button", { name: "С" });
    expect(trigger).toHaveTextContent("22.09.2026");
    fireEvent.click(trigger);

    expect(screen.getByRole("dialog")).toBeVisible();
    expect(screen.getByText("сентябрь 2026 г.")).toBeVisible();
    fireEvent.click(screen.getByRole("gridcell", { name: "23 сентября 2026 г." }));
    fireEvent.click(screen.getByRole("button", { name: "Применить" }));

    expect(onChange).toHaveBeenCalledWith("2026-09-23");
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it("clears the value and closes on Escape", async () => {
    const onChange = vi.fn();
    render(<DatePicker id="to" label="По" value="2026-09-22" onChange={onChange} />);

    const trigger = screen.getByRole("button", { name: "По" });
    fireEvent.click(trigger);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(trigger).toHaveFocus());

    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("button", { name: "Очистить" }));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("disables days outside min and max", () => {
    render(<DatePicker id="bounded" label="Дата" value="2026-09-15" min="2026-09-10" max="2026-09-20" onChange={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: "Дата" }));

    expect(screen.getByRole("gridcell", { name: "9 сентября 2026 г." })).toBeDisabled();
    expect(screen.getByRole("gridcell", { name: "10 сентября 2026 г." })).not.toBeDisabled();
    expect(screen.getByRole("gridcell", { name: "21 сентября 2026 г." })).toBeDisabled();
  });

  it("drafts multiple dates across months and commits only on Apply", () => {
    const onChange = vi.fn();
    render(<DatePicker id="dates" label="Выбор дат" mode="multiple" value={["2026-09-22"]} onChange={onChange} maxSelections={14} />);

    fireEvent.click(screen.getByRole("button", { name: "Выбор дат" }));
    fireEvent.click(screen.getByRole("gridcell", { name: "23 сентября 2026 г." }));
    fireEvent.click(screen.getByRole("button", { name: "Следующий месяц" }));
    fireEvent.click(screen.getByRole("gridcell", { name: "5 октября 2026 г." }));
    expect(screen.getByText("3 из 14")).toBeVisible();
    expect(onChange).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Применить" }));
    expect(onChange).toHaveBeenCalledWith(["2026-09-22", "2026-09-23", "2026-10-05"]);
  });

  it("cancels multiple-date drafts on Escape and clears only the draft until Apply", () => {
    const onChange = vi.fn();
    render(<DatePicker id="dates" label="Выбор дат" mode="multiple" value={["2026-09-22"]} onChange={onChange} />);

    fireEvent.click(screen.getByRole("button", { name: "Выбор дат" }));
    fireEvent.click(screen.getByRole("gridcell", { name: "23 сентября 2026 г." }));
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onChange).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Выбор дат" }));
    fireEvent.click(screen.getByRole("button", { name: "Очистить" }));
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Применить" }));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it("prevents selecting more than maxSelections", () => {
    const onChange = vi.fn();
    render(<DatePicker id="dates" label="Выбор дат" mode="multiple" value={["2026-09-22"]} onChange={onChange} maxSelections={1} />);
    fireEvent.click(screen.getByRole("button", { name: "Выбор дат" }));
    const cappedDate = screen.getByRole("gridcell", { name: "23 сентября 2026 г." });
    expect(cappedDate).toHaveAttribute("aria-disabled", "true");
    expect(cappedDate).not.toBeDisabled();
    expect(screen.getByRole("gridcell", { name: "22 сентября 2026 г." })).not.toBeDisabled();
    cappedDate.focus();
    expect(cappedDate).toHaveFocus();
    fireEvent.keyDown(cappedDate, { key: "Enter" });
    expect(onChange).not.toHaveBeenCalled();
  });
});
